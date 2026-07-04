package cfapi

import (
	"context"
	"fmt"
	"strings"

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
