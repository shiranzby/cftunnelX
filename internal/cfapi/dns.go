package cfapi

import (
	"context"
	"fmt"
	"strings"

	cf "github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/dns"
)

// CreateCNAME 创建 CNAME 记录指向隧道
func (c *Client) CreateCNAME(ctx context.Context, zoneID, name, target string) (string, error) {
	record, err := c.api.DNS.Records.New(ctx, dns.RecordNewParams{
		ZoneID: cf.F(zoneID),
		Body: dns.CNAMERecordParam{
			Name:    cf.F(name),
			Content: cf.F(target),
			Type:    cf.F(dns.CNAMERecordTypeCNAME),
			TTL:     cf.F(dns.TTL(1)),
			Proxied: cf.F(true),
		},
	})
	if err != nil {
		return "", fmt.Errorf("创建 CNAME 记录失败: %w", err)
	}
	return record.ID, nil
}

// DeleteDNSRecord 删除 DNS 记录
func (c *Client) DeleteDNSRecord(ctx context.Context, zoneID, recordID string) error {
	_, err := c.api.DNS.Records.Delete(ctx, recordID, dns.RecordDeleteParams{
		ZoneID: cf.F(zoneID),
	})
	if err != nil {
		return fmt.Errorf("删除 DNS 记录失败: %w", err)
	}
	return nil
}

func IsDNSRecordNotFound(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "81044") ||
		strings.Contains(s, "Record does not exist") ||
		strings.Contains(s, "404 Not Found")
}

// FindDNSRecord 查找指定域名的 DNS 记录
func (c *Client) FindDNSRecord(ctx context.Context, zoneID, name string) (string, error) {
	records, err := c.api.DNS.Records.List(ctx, dns.RecordListParams{
		ZoneID: cf.F(zoneID),
		Name: cf.F(dns.RecordListParamsName{
			Exact: cf.F(name),
		}),
	})
	if err != nil {
		return "", fmt.Errorf("查询 DNS 记录失败: %w", err)
	}
	for _, r := range records.Result {
		if r.Name == name {
			return r.ID, nil
		}
	}
	return "", nil // 未找到
}

// UpdateCNAME 更新 CNAME 记录
func (c *Client) UpdateCNAME(ctx context.Context, zoneID, recordID, name, target string) error {
	_, err := c.api.DNS.Records.Update(ctx, recordID, dns.RecordUpdateParams{
		ZoneID: cf.F(zoneID),
		Body: dns.CNAMERecordParam{
			Name:    cf.F(name),
			Content: cf.F(target),
			Type:    cf.F(dns.CNAMERecordTypeCNAME),
			TTL:     cf.F(dns.TTL(1)),
			Proxied: cf.F(true),
		},
	})
	if err != nil {
		return fmt.Errorf("更新 CNAME 记录失败: %w", err)
	}
	return nil
}
