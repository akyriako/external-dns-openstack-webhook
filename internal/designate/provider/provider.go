/*
gopyright 2017 The Kubernetes Authors.
Copyright 2026 T-Systems International GmbH.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package provider

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/opentelekomcloud/gophertelekomcloud/openstack/dns/v2/recordsets"
	"github.com/opentelekomcloud/gophertelekomcloud/openstack/dns/v2/zones"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
	"sigs.k8s.io/external-dns/provider"
)

import "external-dns-openstack-webhook/internal/designate/client"

type ZoneType string

const (
	ZoneTypePublic  ZoneType = "public"
	ZoneTypePrivate ZoneType = "private"

	// ID of the RecordSet from which endpoint was created
	designateRecordSetID = "designate-recordset-id"
	// Zone ID of the RecordSet
	designateZoneID = "designate-record-id"
	// Zone Type of the RecordSet
	designateZoneType = "designate-zone-type"

	// Initial records values of the RecordSet. This label is required in order not to loose records that haven't
	// changed where there are several targets per domain and only some of them changed.
	// Values are joined by zero-byte to in order to get a single string
	designateOriginalRecords = "designate-original-records"

	// provider-specific key, it will be automatically prefixed with external-dns.alpha.kubernetes.io/
	zoneTypeCustomAnnotationKey          = "webhook/zone-type"
	zoneTypeCustomAnnotationDefaultValue = ZoneTypePublic
)

// designate provider type
type designateProvider struct {
	provider.BaseProvider
	client client.DesignateClientInterface

	// only consider hosted zones managing domains ending in this suffix
	domainFilter endpoint.DomainFilter
	dryRun       bool
}

// NewDesignateProvider is a factory function for OpenStack designate providers
func NewDesignateProvider(domainFilter endpoint.DomainFilter, dryRun bool) (provider.Provider, error) {
	c, err := client.NewDesignateClient()
	if err != nil {
		return nil, err
	}
	return &designateProvider{
		client:       c,
		domainFilter: domainFilter,
		dryRun:       dryRun,
	}, nil
}

// converts domain names to FQDN
func canonicalizeDomainNames(domains []string) []string {
	var cDomains []string
	for _, d := range domains {
		if !strings.HasSuffix(d, ".") {
			d += "."
			cDomains = append(cDomains, strings.ToLower(d))
		}
	}
	return cDomains
}

// converts domain name to FQDN
func canonicalizeDomainName(d string) string {
	if !strings.HasSuffix(d, ".") {
		d += "."
	}
	return strings.ToLower(d)
}

// returns ZoneID -> ZoneName mapping for zones that are managed by the Designate and match domain filter
func (p designateProvider) getZones(ctx context.Context, zoneType string) (map[string]string, error) {
	result := map[string]string{}

	err := p.client.ForEachZone(ctx, zoneType, func(zone *zones.Zone) error {
		//slog.Info("getting zone", "zone", zone.Name, "zone_type", zone.ZoneType)

		if zone.Status == "DELETE" || !zoneMatchesVisibility(zone, zoneType) {
			return nil
		}

		zoneName := canonicalizeDomainName(zone.Name)
		if !p.domainFilter.Match(zoneName) {
			return nil
		}
		result[zone.ID] = zoneName
		return nil
	},
	)

	return result, err
}

func zoneMatchesVisibility(zone *zones.Zone, zoneType string) bool {
	if zoneType == "" || zone.ZoneType == "" {
		return true
	}
	return strings.EqualFold(zone.ZoneType, zoneType)
}

func getZoneType(ep *endpoint.Endpoint) (ZoneType, error) {
	zoneType := zoneTypeCustomAnnotationDefaultValue

	if value, ok := ep.GetProviderSpecificProperty(zoneTypeCustomAnnotationKey); ok {
		zoneType = ZoneType(strings.ToLower(strings.TrimSpace(value)))
	} else if value, ok := ep.Labels[designateZoneType]; ok {
		zoneType = ZoneType(strings.ToLower(strings.TrimSpace(value)))
	}

	switch zoneType {
	case ZoneTypePublic, ZoneTypePrivate:
		return zoneType, nil
	default:
		return "", fmt.Errorf(
			"invalid %s: %q (allowed: %s, %s)",
			zoneTypeCustomAnnotationKey,
			zoneType,
			ZoneTypePublic,
			ZoneTypePrivate,
		)
	}
}

// finds the best suitable DNS zone for the hostname
func getHostZoneID(hostname string, managedZones map[string]string) string {
	longestZoneLength := 0
	resultID := ""

	for zoneID, zoneName := range managedZones {
		if !strings.HasSuffix(hostname, "."+zoneName) && hostname != zoneName {
			continue
		}
		ln := len(zoneName)
		if ln > longestZoneLength {
			resultID = zoneID
			longestZoneLength = ln
		}
	}

	return resultID
}

// Records returns the list of records.
func (p designateProvider) Records(ctx context.Context) ([]*endpoint.Endpoint, error) {
	var result []*endpoint.Endpoint

	for _, zoneType := range []ZoneType{ZoneTypePublic, ZoneTypePrivate} {
		managedZones, err := p.getZones(ctx, string(zoneType))
		if err != nil {
			return nil, err
		}

		for zoneID := range managedZones {
			err = p.client.ForEachRecordSet(ctx, zoneID,
				func(recordSet *recordsets.RecordSet) error {
					if recordSet.Type != endpoint.RecordTypeA && recordSet.Type != endpoint.RecordTypeTXT && recordSet.Type != endpoint.RecordTypeCNAME {
						return nil
					}

					ep := endpoint.NewEndpointWithTTL(recordSet.Name, recordSet.Type, endpoint.TTL(recordSet.TTL), recordSet.Records...)
					ep.Labels[designateRecordSetID] = recordSet.ID
					ep.Labels[designateZoneID] = recordSet.ZoneID
					ep.Labels[designateZoneType] = string(zoneType)
					ep.Labels[designateOriginalRecords] = strings.Join(recordSet.Records, "\000")

					ep.ProviderSpecific = append(ep.ProviderSpecific, endpoint.ProviderSpecificProperty{
						Name:  zoneTypeCustomAnnotationKey,
						Value: string(zoneType),
					})

					result = append(result, ep)

					return nil
				},
			)
			if err != nil {
				return nil, err
			}
		}
	}

	return result, nil
}

// temporary structure to hold recordset parameters so that we could aggregate endpoints into recordsets
type recordSet struct {
	dnsName     string
	recordType  string
	zoneID      string
	recordSetID string
	ttl         int
	names       map[string]bool
	zoneType    string
}

// adds endpoint into recordset aggregation, loading original values from endpoint labels first
func addEndpoint(ep *endpoint.Endpoint, recordSets map[string]*recordSet, oldEndpoints []*endpoint.Endpoint, delete bool) error {
	addDesignateMetadataFromExistingEndpoints(oldEndpoints, ep)

	zoneType, err := getZoneType(ep)
	if err != nil {
		return err
	}

	key := fmt.Sprintf("%s/%s/%s", ep.DNSName, ep.RecordType, zoneType)

	rs := recordSets[key]
	if rs == nil {
		rs = &recordSet{
			dnsName:    canonicalizeDomainName(ep.DNSName),
			recordType: ep.RecordType,
			names:      make(map[string]bool),
			zoneType:   string(zoneType),
		}
	}

	if rs.zoneID == "" {
		rs.zoneID = ep.Labels[designateZoneID]
	}
	if rs.recordSetID == "" {
		rs.recordSetID = ep.Labels[designateRecordSetID]
	}

	rs.ttl = int(ep.RecordTTL)

	for _, rec := range strings.Split(ep.Labels[designateOriginalRecords], "\000") {
		if _, ok := rs.names[rec]; !ok && rec != "" {
			rs.names[rec] = true
		}
	}

	targets := ep.Targets
	if ep.RecordType == endpoint.RecordTypeCNAME {
		targets = canonicalizeDomainNames(targets)
	}

	for _, t := range targets {
		rs.names[t] = !delete
	}

	recordSets[key] = rs
	return nil
}

// addDesignateMetadataFromExistingEndpoints adds the labels identified by the constants designateZoneID and designateRecordSetID
// to an Endpoint. Therefore, it searches all given existing endpoints for an endpoint with the same record type and record
// value. If the given Endpoint already has the labels set, they are left untouched. This fixes an issue with the
// TXTRegistry which generates new TXT entries instead of updating the old ones.
func addDesignateMetadataFromExistingEndpoints(existingEndpoints []*endpoint.Endpoint, ep *endpoint.Endpoint) {
	_, hasZoneIDLabel := ep.Labels[designateZoneID]
	_, hasRecordSetIDLabel := ep.Labels[designateRecordSetID]
	_, hasZoneType := ep.GetProviderSpecificProperty(zoneTypeCustomAnnotationKey)
	_, hasZoneTypeLabel := ep.Labels[designateZoneType]

	if hasZoneIDLabel && hasRecordSetIDLabel && hasZoneType {
		return
	}

	for _, oep := range existingEndpoints {
		if ep.RecordType != oep.RecordType || ep.DNSName != oep.DNSName {
			continue
		}

		if desiredZoneType, ok := ep.GetProviderSpecificProperty(zoneTypeCustomAnnotationKey); ok {
			existingZoneType, ok := oep.GetProviderSpecificProperty(zoneTypeCustomAnnotationKey)
			if !ok || existingZoneType != desiredZoneType {
				continue
			}
		}

		if !hasZoneIDLabel {
			ep.Labels[designateZoneID] = oep.Labels[designateZoneID]
		}

		if !hasRecordSetIDLabel {
			ep.Labels[designateRecordSetID] = oep.Labels[designateRecordSetID]
		}

		if !hasZoneType {
			if value, ok := oep.GetProviderSpecificProperty(zoneTypeCustomAnnotationKey); ok {
				ep.ProviderSpecific = append(ep.ProviderSpecific, endpoint.ProviderSpecificProperty{
					Name:  zoneTypeCustomAnnotationKey,
					Value: value,
				})

				if !hasZoneTypeLabel {
					if value, ok := oep.Labels[designateZoneType]; ok {
						ep.Labels[designateZoneType] = value
					}
				}
			}
		}

		return
	}
}

// ApplyChanges applies a given set of changes in a given zone.
func (p designateProvider) ApplyChanges(ctx context.Context, changes *plan.Changes) error {
	endpoints, err := p.Records(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch active records: %w", err)
	}

	recordSets := map[string]*recordSet{}

	for _, ep := range changes.Create {
		if err := addEndpoint(ep, recordSets, endpoints, false); err != nil {
			return err
		}
	}
	for _, ep := range changes.UpdateOld {
		if err := addEndpoint(ep, recordSets, endpoints, true); err != nil {
			return err
		}
	}
	for _, ep := range changes.UpdateNew {
		if err := addEndpoint(ep, recordSets, endpoints, false); err != nil {
			return err
		}
	}
	for _, ep := range changes.Delete {
		if err := addEndpoint(ep, recordSets, endpoints, true); err != nil {
			return err
		}
	}

	managedZonesByType := map[string]map[string]string{}

	for _, rs := range recordSets {
		managedZones, ok := managedZonesByType[rs.zoneType]
		if !ok {
			var err2 error
			managedZones, err2 = p.getZones(ctx, rs.zoneType)
			if err2 != nil {
				if err == nil {
					err = err2
				}
				continue
			}

			managedZonesByType[rs.zoneType] = managedZones
		}

		if err2 := p.upsertRecordSet(ctx, rs, managedZones); err == nil {
			err = err2
		}
	}

	return err
}

// apply recordset changes by inserting/updating/deleting recordsets
func (p designateProvider) upsertRecordSet(ctx context.Context, rs *recordSet, managedZones map[string]string) error {
	//managedZones, err := p.getZones(ctx, rs.zoneType)
	//if err != nil {
	//	return err
	//}

	slog.Info("upserting recordset", "record", rs.dnsName, "record_type", rs.recordType, "zone_type", rs.zoneType)

	if rs.zoneID == "" {
		rs.zoneID = getHostZoneID(rs.dnsName, managedZones)
		if rs.zoneID == "" {
			slog.Debug("upserting record skipped: no matching zone detected", "record", rs.dnsName, "zone_type", rs.zoneType)
			return nil
		}

		if _, ok := managedZones[rs.zoneID]; !ok {
			return fmt.Errorf(
				"refusing to modify record %s/%s: zoneID %s does not belong to zone type %s",
				rs.dnsName,
				rs.recordType,
				rs.zoneID,
				rs.zoneType,
			)
		}
	}

	var records []string
	for rec, v := range rs.names {
		if v {
			records = append(records, rec)
		}
	}
	if rs.recordSetID == "" && records == nil {
		return nil
	}
	if rs.recordSetID == "" {
		opts := recordsets.CreateOpts{
			Name:    rs.dnsName,
			Type:    rs.recordType,
			Records: records,
			TTL:     rs.ttl,
		}
		slog.Info("creating records", "record", rs.dnsName, "record_type", rs.recordType, "record_value", strings.Join(records, ","))
		if p.dryRun {
			return nil
		}
		_, err := p.client.CreateRecordSet(ctx, rs.zoneID, opts)
		return err
	} else if len(records) == 0 {
		slog.Info("deleting records", "record", rs.dnsName, "record_type", rs.recordType)
		if p.dryRun {
			return nil
		}
		return p.client.DeleteRecordSet(ctx, rs.zoneID, rs.recordSetID)
	} else {
		opts := recordsets.UpdateOpts{
			Records: records,
			TTL:     rs.ttl,
		}
		slog.Info("updating records", "record", rs.dnsName, "record_type", rs.recordType, "record_value", strings.Join(records, ","))
		if p.dryRun {
			return nil
		}
		return p.client.UpdateRecordSet(ctx, rs.zoneID, rs.recordSetID, opts)
	}
}
