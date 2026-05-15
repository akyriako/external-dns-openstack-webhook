/*
Copyright 2017 The Kubernetes Authors.
Copyright 2024 inovex GmbH.

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

package client

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"external-dns-openstack-webhook/internal/metrics"

	golangsdk "github.com/opentelekomcloud/gophertelekomcloud"
	"github.com/opentelekomcloud/gophertelekomcloud/openstack"
	"github.com/opentelekomcloud/gophertelekomcloud/openstack/dns/v2/recordsets"
	"github.com/opentelekomcloud/gophertelekomcloud/openstack/dns/v2/zones"
	"github.com/opentelekomcloud/gophertelekomcloud/pagination"
)

// DesignateClientInterface interface between provider and OpenStack DNS API
type DesignateClientInterface interface {
	// ForEachZone calls handler for each zone managed by the Designate
	ForEachZone(ctx context.Context, zoneType string, handler func(zone *zones.Zone) error) error

	// ForEachRecordSet calls handler for each recordset in the given DNS zone
	ForEachRecordSet(ctx context.Context, zoneID string, handler func(recordSet *recordsets.RecordSet) error) error

	// CreateRecordSet creates recordset in the given DNS zone
	CreateRecordSet(ctx context.Context, zoneID string, opts recordsets.CreateOpts) (string, error)

	// UpdateRecordSet updates recordset in the given DNS zone
	UpdateRecordSet(ctx context.Context, zoneID, recordSetID string, opts recordsets.UpdateOpts) error

	// DeleteRecordSet deletes recordset in the given DNS zone
	DeleteRecordSet(ctx context.Context, zoneID, recordSetID string) error
}

// implementation of the DesignateClientInterface
type designateClient struct {
	serviceClient *golangsdk.ServiceClient
}

// NewDesignateClient factory function for the DesignateClientInterface
func NewDesignateClient() (DesignateClientInterface, error) {
	serviceClient, err := createDesignateServiceClient()
	if err != nil {
		return nil, err
	}
	return &designateClient{serviceClient}, nil
}

// authenticate in OpenStack and obtain Designate service endpoint
func createDesignateServiceClient() (*golangsdk.ServiceClient, error) {
	env := openstack.NewEnv("OS_")
	cloud, err := env.Cloud()
	if err != nil {
		return nil, err
	}

	providerClient, err := openstack.AuthenticatedClientFromCloud(cloud)
	if err != nil {
		return nil, err
	}
	slog.Info("using T-Cloud Public IAM", "addr", providerClient.IdentityEndpoint)

	endpointOptions := golangsdk.EndpointOpts{Region: cloud.RegionName}
	if availability := cloud.EndpointType; availability != "" {
		endpointOptions.Availability = golangsdk.Availability(availability)
	} else if cloud.Interface != "" {
		endpointOptions.Availability = golangsdk.Availability(cloud.Interface)
	}

	client, err := openstack.NewDNSV2(providerClient, endpointOptions)
	if err != nil {
		return nil, err
	}
	slog.Info("using T-Cloud Public DNS", "addr", client.Endpoint)
	return client, nil
}

// ForEachZone calls handler for each zone managed by the Designate
func (c designateClient) ForEachZone(ctx context.Context, zoneType string, handler func(zone *zones.Zone) error) error {
	startTime := time.Now()

	pager := zones.List(c.serviceClient, zones.ListOpts{
		Type: zoneType,
	})

	var pageCount int
	var zoneCount int

	err := pager.EachPage(
		func(page pagination.Page) (bool, error) {
			// Each page corresponds to a separate API call.
			pageCount++
			metrics.TotalApiCalls.Inc()

			list, err := zones.ExtractZones(page)
			if err != nil {
				return false, err
			}

			zoneCount += len(list)

			for _, zone := range list {
				err := handler(&zone)
				if err != nil {
					return false, err
				}
			}
			return true, nil
		},
	)

	duration := time.Since(startTime)
	metrics.ApiCallLatency.WithLabelValues("ForEachZone").Observe(duration.Seconds())

	if err != nil {
		metrics.FailedApiCallsTotal.Inc()
		slog.Error(fmt.Sprintf("getting recordsets failed: %v", err), "zone_type", zoneType, "duration", duration)
	} else {
		slog.Debug("getting recordsets completed", "zone_type", zoneType, "zones_count", zoneCount, "page_count", pageCount, "duration", duration)
	}

	return err
}

// ForEachRecordSet calls handler for each recordset in the given DNS zone
func (c designateClient) ForEachRecordSet(ctx context.Context, zoneID string, handler func(recordSet *recordsets.RecordSet) error) error {
	startTime := time.Now()

	pager := recordsets.ListByZone(c.serviceClient, zoneID, recordsets.ListOpts{})
	var pageCount int
	var recordCount int

	err := pager.EachPage(
		func(page pagination.Page) (bool, error) {
			// Each page corresponds to a separate API call.
			pageCount++
			metrics.TotalApiCalls.Inc()

			list, err := recordsets.ExtractRecordSets(page)
			if err != nil {
				return false, err
			}

			recordCount += len(list)

			for _, recordSet := range list {
				err := handler(&recordSet)
				if err != nil {
					return false, err
				}
			}
			return true, nil
		},
	)

	duration := time.Since(startTime)
	metrics.ApiCallLatency.WithLabelValues("ForEachRecordSet").Observe(duration.Seconds())

	if err != nil {
		metrics.FailedApiCallsTotal.Inc()
		slog.Error(fmt.Sprintf("getting records failed: %v", err), "zone_id", zoneID, "duration", duration)
	} else {
		slog.Debug("getting records completed", "zone_id", zoneID, "record_count", recordCount, "page_count", pageCount, "duration", duration)
	}

	return err
}

// CreateRecordSet creates recordset in the given DNS zone
func (c designateClient) CreateRecordSet(ctx context.Context, zoneID string, opts recordsets.CreateOpts) (string, error) {
	startTime := time.Now()
	metrics.TotalApiCalls.Inc()

	slog.Debug("creating recordset", "record_set", opts.Name, "recordset_type", opts.Type, "targets", len(opts.Records))

	r, err := recordsets.Create(c.serviceClient, zoneID, opts).Extract()

	duration := time.Since(startTime)
	metrics.ApiCallLatency.WithLabelValues("CreateRecordSet").Observe(duration.Seconds())

	if err != nil {
		metrics.FailedApiCallsTotal.Inc()
		slog.Error(fmt.Sprintf("creating recordset failed: %v", err), "record_set", opts.Name, "duration", duration)
		return "", err
	}

	slog.Debug("created recordset", "record_set", opts.Name, "record_set_id", r.ID, "duration", duration)
	return r.ID, nil
}

// UpdateRecordSet updates recordset in the given DNS zone
func (c designateClient) UpdateRecordSet(ctx context.Context, zoneID, recordSetID string, opts recordsets.UpdateOpts) error {
	startTime := time.Now()
	metrics.TotalApiCalls.Inc()

	recordCount := 0
	if opts.Records != nil {
		recordCount = len(opts.Records)
	}
	slog.Debug("updating recordset", "record-set-id", recordSetID, "targets", recordCount)

	_, err := recordsets.Update(c.serviceClient, zoneID, recordSetID, opts).Extract()

	duration := time.Since(startTime)
	metrics.ApiCallLatency.WithLabelValues("UpdateRecordSet").Observe(duration.Seconds())

	if err != nil {
		metrics.FailedApiCallsTotal.Inc()
		slog.Error(fmt.Sprintf("updating recordset failed: %v", err), "record-set-id", recordSetID, "duration", duration)
	} else {
		slog.Debug("updated recordset", "record-set-id", recordSetID, "duration", duration)
	}

	return err
}

// DeleteRecordSet deletes recordset in the given DNS zone
func (c designateClient) DeleteRecordSet(ctx context.Context, zoneID, recordSetID string) error {
	startTime := time.Now()
	metrics.TotalApiCalls.Inc()

	slog.Debug("deleting recordset", "record-set-id", recordSetID)

	err := recordsets.Delete(c.serviceClient, zoneID, recordSetID).ExtractErr()

	duration := time.Since(startTime)
	metrics.ApiCallLatency.WithLabelValues("DeleteRecordSet").Observe(duration.Seconds())

	if err != nil {
		metrics.FailedApiCallsTotal.Inc()
		slog.Error(fmt.Sprintf("deleting recordset failed: %v", err), "record-set-id", recordSetID, "duration", duration)
	} else {
		slog.Debug("deleted recordset", "record-set-id", recordSetID, "duration", duration)
	}

	return err
}
