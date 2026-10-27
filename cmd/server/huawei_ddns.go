package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/config"
	dns "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dns/v2"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dns/v2/model"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dns/v2/region"
	"github.com/hainiu6/tz/internal/store"
)

type huaweiDNSClient interface {
	ListPublicZones(*model.ListPublicZonesRequest) (*model.ListPublicZonesResponse, error)
	ListRecordSetsByZone(*model.ListRecordSetsByZoneRequest) (*model.ListRecordSetsByZoneResponse, error)
	CreateRecordSet(*model.CreateRecordSetRequest) (*model.CreateRecordSetResponse, error)
	UpdateRecordSet(*model.UpdateRecordSetRequest) (*model.UpdateRecordSetResponse, error)
}

func newHuaweiDNSClient(ak, sk string) (huaweiDNSClient, error) {
	auth, err := basic.NewCredentialsBuilder().WithAk(ak).WithSk(sk).SafeBuild()
	if err != nil {
		return nil, err
	}
	// Public DNS zones are global resources; Huawei documents cn-north-4 for them.
	location, err := region.SafeValueOf("cn-north-4")
	if err != nil {
		return nil, err
	}
	hc, err := dns.DnsClientBuilder().WithRegion(location).WithCredential(auth).
		WithHttpConfig(config.DefaultHttpConfig().WithTimeout(20 * time.Second)).SafeBuild()
	if err != nil {
		return nil, err
	}
	return dns.NewDnsClient(hc), nil
}

func dnsValue(value string) string { return strings.TrimSuffix(strings.ToLower(value), ".") }

func updateHuaweiRecord(client huaweiDNSClient, task store.DDNSTask) (string, string, error) {
	domain := task.Domain + "."
	limit := int32(500)
	var zoneID, zoneName string
	for offset := int32(0); ; offset += limit {
		zones, err := client.ListPublicZones(&model.ListPublicZonesRequest{Limit: &limit, Offset: &offset})
		if err != nil {
			return "", "", fmt.Errorf("查询公网域名失败: %w", err)
		}
		if zones.Zones == nil {
			break
		}
		for _, zone := range *zones.Zones {
			if zone.Name == nil || zone.Id == nil {
				continue
			}
			name := dnsValue(*zone.Name)
			if (task.Domain == name || strings.HasSuffix(task.Domain, "."+name)) && len(name) > len(zoneName) {
				zoneID, zoneName = *zone.Id, name
			}
		}
		if len(*zones.Zones) < int(limit) {
			break
		}
	}
	if zoneID == "" {
		return "", "", errors.New("华为云账号下找不到此域名所属的公网域名")
	}
	mode, recordType := "equal", task.Type
	var matches []model.ListRecordSets
	for offset := int32(0); ; offset += limit {
		response, err := client.ListRecordSetsByZone(&model.ListRecordSetsByZoneRequest{
			ZoneId: zoneID, SearchMode: &mode, Name: &domain, Type: &recordType, Limit: &limit, Offset: &offset,
		})
		if err != nil {
			return "", "", fmt.Errorf("查询解析记录失败: %w", err)
		}
		if response.Recordsets == nil {
			break
		}
		for _, record := range *response.Recordsets {
			if record.Name != nil && record.Type != nil && dnsValue(*record.Name) == task.Domain && *record.Type == task.Type {
				matches = append(matches, record)
			}
		}
		if len(*response.Recordsets) < int(limit) {
			break
		}
	}
	if len(matches) > 1 {
		return "", "", errors.New("存在多条同名同类型记录，无法安全选择要修改的记录")
	}
	value := task.Target
	if task.Type == "CNAME" {
		value += "."
	}
	if len(matches) == 0 {
		_, err := client.CreateRecordSet(&model.CreateRecordSetRequest{ZoneId: zoneID, Body: &model.CreateRecordSetRequestBody{
			Name: domain, Type: task.Type, Records: []string{value},
		}})
		if err != nil {
			return "", "", fmt.Errorf("创建解析记录失败: %w", err)
		}
		return "", "created", nil
	}
	record := matches[0]
	if record.Id == nil {
		return "", "", errors.New("华为云未返回解析记录 ID")
	}
	old := ""
	if record.Records != nil {
		old = strings.Join(*record.Records, ", ")
	}
	if record.Records != nil && len(*record.Records) == 1 && dnsValue((*record.Records)[0]) == dnsValue(value) {
		return old, "unchanged", nil
	}
	values := []string{value}
	_, err := client.UpdateRecordSet(&model.UpdateRecordSetRequest{ZoneId: zoneID, RecordsetId: *record.Id, Body: &model.UpdateRecordSetReq{Records: &values}})
	if err != nil {
		return old, "", fmt.Errorf("更新解析记录失败: %w", err)
	}
	return old, "updated", nil
}
