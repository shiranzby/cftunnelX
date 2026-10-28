package cfapi

import (
	"context"
	"fmt"
	"strings"
	"time"

	cf "github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/shared"
	"github.com/cloudflare/cloudflare-go/v6/zero_trust"
)

// CreateTunnel 创建 Cloudflare Tunnel
func (c *Client) CreateTunnel(ctx context.Context, name string) (*shared.CloudflareTunnel, error) {
	tunnel, err := c.api.ZeroTrust.Tunnels.Cloudflared.New(ctx, zero_trust.TunnelCloudflaredNewParams{
		AccountID: cf.F(c.accountID),
		Name:      cf.F(name),
		ConfigSrc: cf.F(zero_trust.TunnelCloudflaredNewParamsConfigSrcCloudflare),
	})
	if err != nil {
		return nil, fmt.Errorf("创建隧道失败: %w", err)
	}
	return tunnel, nil
}

// DeleteTunnel 删除隧道
func (c *Client) DeleteTunnel(ctx context.Context, tunnelID string) error {
	_, err := c.api.ZeroTrust.Tunnels.Cloudflared.Delete(ctx, tunnelID, zero_trust.TunnelCloudflaredDeleteParams{
		AccountID: cf.F(c.accountID),
	})
	if err != nil {
		return fmt.Errorf("删除隧道失败: %w", err)
	}
	return nil
}

func IsTunnelActiveConnections(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "1022") ||
		strings.Contains(s, "active connections")
}

func IsTunnelNotFound(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "404 Not Found") ||
		strings.Contains(s, "not found") ||
		strings.Contains(s, "does not exist")
}

// IsAuthError 判断错误是否为凭据/权限问题。
//
// 必须与"网络不可达"区分开：健康探测器在 API 调用失败后会去看边缘是否可达，
// 若边缘可达就判定为 stalled 并触发重启。但权限不足时边缘当然是可达的，
// 于是"Token 没给 Zero Trust 读权限"会被误判成"隧道卡死"，
// 导致每过一个冷却周期就无谓重启一次健康的隧道。
// 因此这类错误一律按 unknown 处理，不做任何自愈动作。
func IsAuthError(err error) bool {
	if err == nil {
		return false
	}
	// 大小写不敏感：Cloudflare 不同接口返回的文案并不统一
	// （"Invalid API Token" / "Authentication error" / http 状态码都可能出现）
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "401") ||
		strings.Contains(s, "403") ||
		strings.Contains(s, "authentication error") ||
		strings.Contains(s, "unauthorized") ||
		strings.Contains(s, "forbidden") ||
		strings.Contains(s, "invalid token") ||
		strings.Contains(s, "invalid api token") ||
		strings.Contains(s, "10000")
}

// ListTunnels 列出所有隧道
func (c *Client) ListTunnels(ctx context.Context) ([]shared.CloudflareTunnel, error) {
	pager := c.api.ZeroTrust.Tunnels.Cloudflared.ListAutoPaging(ctx, zero_trust.TunnelCloudflaredListParams{
		AccountID: cf.F(c.accountID),
	})
	var result []shared.CloudflareTunnel
	for pager.Next() {
		result = append(result, pager.Current())
	}
	if err := pager.Err(); err != nil {
		return nil, fmt.Errorf("列出隧道失败: %w", err)
	}
	return result, nil
}

// PushIngressConfig 推送 ingress 配置到 Cloudflare 远端
func (c *Client) PushIngressConfig(ctx context.Context, tunnelID string, routes []IngressRule) error {
	// 添加 catch-all 规则
	ingress := make([]zero_trust.TunnelCloudflaredConfigurationUpdateParamsConfigIngress, 0, len(routes)+1)
	for _, r := range routes {
		ingress = append(ingress, zero_trust.TunnelCloudflaredConfigurationUpdateParamsConfigIngress{
			Hostname: cf.F(r.Hostname),
			Service:  cf.F(r.Service),
		})
	}
	ingress = append(ingress, zero_trust.TunnelCloudflaredConfigurationUpdateParamsConfigIngress{
		Service: cf.F("http_status:404"),
	})

	_, err := c.api.ZeroTrust.Tunnels.Cloudflared.Configurations.Update(ctx, tunnelID, zero_trust.TunnelCloudflaredConfigurationUpdateParams{
		AccountID: cf.F(c.accountID),
		Config: cf.F(zero_trust.TunnelCloudflaredConfigurationUpdateParamsConfig{
			Ingress: cf.F(ingress),
		}),
	})
	if err != nil {
		return fmt.Errorf("推送 ingress 配置失败: %w", err)
	}
	return nil
}

// IngressRule ingress 路由规则
type IngressRule struct {
	Hostname string
	Service  string
}

// GetTunnelToken 获取隧道运行 Token
func (c *Client) GetTunnelToken(ctx context.Context, tunnelID string) (string, error) {
	token, err := c.api.ZeroTrust.Tunnels.Cloudflared.Token.Get(ctx, tunnelID, zero_trust.TunnelCloudflaredTokenGetParams{
		AccountID: cf.F(c.accountID),
	})
	if err != nil {
		return "", fmt.Errorf("获取隧道 Token 失败: %w", err)
	}
	return *token, nil
}

// TunnelConnection 表示一条**活动**的隧道连接（一个 cloudflared 到边缘的 connIndex）。
type TunnelConnection struct {
	ColoName string    // 接入的 Cloudflare 数据中心，如 lax13
	OpenedAt time.Time // 该连接建立时间
}

// GetTunnelConnections 查询指定隧道当前活动的连接列表。
//
// 关键语义（供上层判断健康度）：
//   - 返回 nil error 且空切片 ⇒ API 可达，但隧道**没有任何连接**（stalled）。
//   - 返回 error ⇒ API 本身不可达（网络阻断 / DNS / 鉴权），
//     无法据此判断隧道好坏，上层需用边缘连通性进一步区分 blocked 与 stalled。
//
// Cloudflare 在连接断开后仍会短期保留记录（IsPendingReconnect=true），
// 这些不计入活动连接，否则会把"已断但未过期"误判成健康。
func (c *Client) GetTunnelConnections(ctx context.Context, tunnelID string) ([]TunnelConnection, error) {
	page, err := c.api.ZeroTrust.Tunnels.Cloudflared.Connections.Get(ctx, tunnelID,
		zero_trust.TunnelCloudflaredConnectionGetParams{
			AccountID: cf.F(c.accountID),
		})
	if err != nil {
		return nil, fmt.Errorf("查询隧道连接失败: %w", err)
	}

	out := make([]TunnelConnection, 0, len(page.Result))
	for _, client := range page.Result {
		for _, conn := range client.Conns {
			if conn.IsPendingReconnect {
				continue
			}
			out = append(out, TunnelConnection{
				ColoName: conn.ColoName,
				OpenedAt: conn.OpenedAt,
			})
		}
	}
	return out, nil
}
