// Mgmt
// Copyright (C) James Shubin and the project contributors
// Written by James Shubin <james@shubin.ca> and the project contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.
//
// Additional permission under GNU GPL version 3 section 7
//
// If you modify this program, or any covered work, by linking or combining it
// with embedded mcl code and modules (and that the embedded mcl code and
// modules which link with this program, contain a copy of their source code in
// the authoritative form) containing parts covered by the terms of any other
// license, the licensors of this program grant you additional permission to
// convey the resulting work. Furthermore, the licensors of this program grant
// the original author, James Shubin, additional permission to update this
// additional permission if he deems it necessary to achieve the goals of this
// additional permission.

package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestCloudflareDNSRecordResCheckApplyCreatesRecord(t *testing.T) {
	t.Parallel()

	fixture := newCloudflareDNSTestFixture(t)
	resource := newCloudflareDNSTestResource(fixture.server.URL)
	resource.ZoneName = "example.test"
	resource.Type = "CNAME"
	resource.RecordName = "api.edge"
	resource.Content = "origin-api.example.net"
	resource.TTL = 300
	resource.Proxied = true
	resource.Comment = "managed by mgmt"
	resource.Tags = []string{"role:api", "env:test"}

	if err := resource.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if err := resource.Init(nil); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	defer resource.Cleanup()

	checkOK, err := resource.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected create to report non-converged after apply")
	}

	checkOK, err = resource.CheckApply(context.Background(), false)
	if err != nil {
		t.Fatalf("second checkapply failed: %v", err)
	}
	if !checkOK {
		t.Fatalf("expected record to be converged after create")
	}

	records := fixture.recordsForZone("zone-1")
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	record := records[0]
	if record.Name != "api.edge.example.test" {
		t.Fatalf("unexpected record name: %s", record.Name)
	}
	if record.Content != "origin-api.example.net" {
		t.Fatalf("unexpected record content: %s", record.Content)
	}
	if !record.Proxied {
		t.Fatalf("expected proxied record")
	}
	if got := strings.Join(record.Tags, ","); got != "env:test,role:api" {
		t.Fatalf("unexpected tags: %s", got)
	}
}

func TestCloudflareDNSRecordResCheckApplyUpdatesRecord(t *testing.T) {
	t.Parallel()

	fixture := newCloudflareDNSTestFixture(t)
	fixture.records["zone-1"] = []cloudflareDNSRecord{
		{
			ID:        "record-1",
			Type:      "A",
			Name:      "api.edge.example.test",
			Content:   "203.0.113.10",
			TTL:       120,
			Proxied:   false,
			Comment:   "old",
			Tags:      []string{"env:test"},
			Proxiable: true,
		},
	}

	resource := newCloudflareDNSTestResource(fixture.server.URL)
	resource.ZoneName = "example.test"
	resource.Type = "A"
	resource.RecordName = "api.edge"
	resource.Content = "203.0.113.22"
	resource.TTL = 300
	resource.Proxied = true
	resource.Comment = "managed by mgmt"
	resource.Tags = []string{"role:api", "env:test"}

	if err := resource.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if err := resource.Init(nil); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	defer resource.Cleanup()

	checkOK, err := resource.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected update to report non-converged after apply")
	}

	records := fixture.recordsForZone("zone-1")
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	record := records[0]
	if record.Content != "203.0.113.22" {
		t.Fatalf("unexpected record content: %s", record.Content)
	}
	if record.TTL != 300 {
		t.Fatalf("unexpected ttl: %d", record.TTL)
	}
	if !record.Proxied {
		t.Fatalf("expected proxied record after update")
	}
	if record.Comment != "managed by mgmt" {
		t.Fatalf("unexpected comment: %s", record.Comment)
	}
}

func TestCloudflareDNSRecordResCheckApplyDeletesRecord(t *testing.T) {
	t.Parallel()

	fixture := newCloudflareDNSTestFixture(t)
	fixture.records["zone-1"] = []cloudflareDNSRecord{
		{
			ID:      "record-1",
			Type:    "TXT",
			Name:    "telemetry.edge.example.test",
			Content: "hello",
			TTL:     300,
		},
		{
			ID:      "record-2",
			Type:    "TXT",
			Name:    "telemetry.edge.example.test",
			Content: "world",
			TTL:     300,
		},
	}

	resource := newCloudflareDNSTestResource(fixture.server.URL)
	resource.ZoneName = "example.test"
	resource.State = CloudflareDNSStateAbsent
	resource.Type = "TXT"
	resource.RecordName = "telemetry.edge"
	resource.Content = "hello"
	resource.TTL = 300

	if err := resource.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if err := resource.Init(nil); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	defer resource.Cleanup()

	checkOK, err := resource.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected delete to report non-converged after apply")
	}

	if got := len(fixture.recordsForZone("zone-1")); got != 0 {
		t.Fatalf("expected no records after delete, got %d", got)
	}
}

func TestCloudflareDNSRecordResCheckApplyCreatesRecordSet(t *testing.T) {
	t.Parallel()

	fixture := newCloudflareDNSTestFixture(t)

	resource := newCloudflareDNSTestResource(fixture.server.URL)
	resource.ZoneName = "example.test"
	resource.Type = "A"
	resource.RecordName = "cdn.edge"
	resource.Contents = []string{"198.51.100.10", "198.51.100.11"}
	resource.TTL = 300
	resource.Proxied = true
	resource.Comment = "managed by mgmt"
	resource.Tags = []string{"service:cdn", "env:test"}

	if err := resource.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if err := resource.Init(nil); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	defer resource.Cleanup()

	checkOK, err := resource.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected create to report non-converged after apply")
	}

	checkOK, err = resource.CheckApply(context.Background(), false)
	if err != nil {
		t.Fatalf("second checkapply failed: %v", err)
	}
	if !checkOK {
		t.Fatalf("expected record set to be converged after create")
	}

	records := fixture.recordsForZone("zone-1")
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if got := cloudflareDNSTestContents(records); got != "198.51.100.10,198.51.100.11" {
		t.Fatalf("unexpected record contents: %s", got)
	}
	for _, record := range records {
		if !record.Proxied {
			t.Fatalf("expected proxied record set")
		}
		if record.Comment != "managed by mgmt" {
			t.Fatalf("unexpected comment: %s", record.Comment)
		}
	}
}

func TestCloudflareDNSRecordResLookupRecordsSinglePagePreservesBehavior(t *testing.T) {
	t.Parallel()

	fixture := newCloudflareDNSTestFixture(t)
	fixture.zones = []cloudflareZone{
		{ID: "zone-1", Name: "example.test"},
	}
	fixture.records["zone-1"] = []cloudflareDNSRecord{
		{
			ID:      "record-1",
			Type:    "A",
			Name:    "cdn.edge.example.test",
			Content: "198.51.100.10",
			TTL:     300,
		},
		{
			ID:      "record-2",
			Type:    "A",
			Name:    "cdn.edge.example.test",
			Content: "198.51.100.11",
			TTL:     300,
		},
		{
			ID:      "record-3",
			Type:    "A",
			Name:    "other.edge.example.test",
			Content: "198.51.100.12",
			TTL:     300,
		},
	}

	resource := newCloudflareDNSTestResource(fixture.server.URL)
	if err := resource.Init(nil); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	defer resource.Cleanup()

	records, err := resource.lookupRecords(context.Background(), "zone-1", "cdn.edge.example.test", "A")
	if err != nil {
		t.Fatalf("lookuprecords failed: %v", err)
	}
	if got := cloudflareDNSTestContents(records); got != "198.51.100.10,198.51.100.11" {
		t.Fatalf("unexpected record contents: %s", got)
	}
	if got := fixture.recordListPages(); !slices.Equal(got, []int{1}) {
		t.Fatalf("unexpected lookup pages: %v", got)
	}
}

func TestCloudflareDNSRecordResCheckApplyReconcilesRecordSet(t *testing.T) {
	t.Parallel()

	fixture := newCloudflareDNSTestFixture(t)
	fixture.records["zone-1"] = []cloudflareDNSRecord{
		{
			ID:        "record-1",
			Type:      "A",
			Name:      "cdn.edge.example.test",
			Content:   "198.51.100.10",
			TTL:       120,
			Proxied:   false,
			Comment:   "old",
			Tags:      []string{"env:test"},
			Proxiable: true,
		},
		{
			ID:        "record-2",
			Type:      "A",
			Name:      "cdn.edge.example.test",
			Content:   "198.51.100.12",
			TTL:       120,
			Proxied:   false,
			Comment:   "old",
			Tags:      []string{"env:test"},
			Proxiable: true,
		},
	}

	resource := newCloudflareDNSTestResource(fixture.server.URL)
	resource.ZoneName = "example.test"
	resource.Type = "A"
	resource.RecordName = "cdn.edge"
	resource.Contents = []string{"198.51.100.10", "198.51.100.11"}
	resource.TTL = 300
	resource.Proxied = true
	resource.Comment = "managed by mgmt"
	resource.Tags = []string{"service:cdn", "env:test"}

	if err := resource.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if err := resource.Init(nil); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	defer resource.Cleanup()

	checkOK, err := resource.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected reconcile to report non-converged after apply")
	}

	checkOK, err = resource.CheckApply(context.Background(), false)
	if err != nil {
		t.Fatalf("second checkapply failed: %v", err)
	}
	if !checkOK {
		t.Fatalf("expected record set to be converged after reconcile")
	}

	records := fixture.recordsForZone("zone-1")
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if got := cloudflareDNSTestContents(records); got != "198.51.100.10,198.51.100.11" {
		t.Fatalf("unexpected record contents: %s", got)
	}
}

func TestCloudflareDNSRecordResCheckApplyReconcilesRecordSetAcrossPages(t *testing.T) {
	t.Parallel()

	fixture := newCloudflareDNSTestFixture(t)
	fixture.zones = []cloudflareZone{
		{ID: "zone-1", Name: "example.test"},
	}
	desiredContents := make([]string, 0, 100)
	fixture.records["zone-1"] = make([]cloudflareDNSRecord, 0, 101)
	for i := 1; i <= 101; i++ {
		content := fmt.Sprintf("198.51.100.%d", i)
		fixture.records["zone-1"] = append(fixture.records["zone-1"], cloudflareDNSRecord{
			ID:        fmt.Sprintf("record-%d", i),
			Type:      "A",
			Name:      "cdn.edge.example.test",
			Content:   content,
			TTL:       300,
			Proxied:   true,
			Comment:   "managed by mgmt",
			Tags:      []string{"env:test", "service:cdn"},
			Proxiable: true,
		})
		if i <= 100 {
			desiredContents = append(desiredContents, content)
		}
	}

	resource := newCloudflareDNSTestResource(fixture.server.URL)
	resource.ZoneName = "example.test"
	resource.Type = "A"
	resource.RecordName = "cdn.edge"
	resource.Contents = desiredContents
	resource.TTL = 300
	resource.Proxied = true
	resource.Comment = "managed by mgmt"
	resource.Tags = []string{"service:cdn", "env:test"}

	if err := resource.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if err := resource.Init(nil); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	defer resource.Cleanup()

	checkOK, err := resource.CheckApply(context.Background(), true)
	if err != nil {
		t.Fatalf("checkapply failed: %v", err)
	}
	if checkOK {
		t.Fatalf("expected reconcile to report non-converged after apply")
	}
	if got := fixture.recordListPages(); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("expected paginated lookup across both pages, got %v", got)
	}

	records := fixture.recordsForZone("zone-1")
	if len(records) != 100 {
		t.Fatalf("expected 100 records after reconcile, got %d", len(records))
	}
	if got := cloudflareDNSTestContents(records); strings.Contains(got, "198.51.100.101") {
		t.Fatalf("unexpected page-2 record retained after reconcile: %s", got)
	}
	createCalls, updateCalls, deleteCalls := fixture.mutationCounts()
	if createCalls != 0 || updateCalls != 0 || deleteCalls != 1 {
		t.Fatalf("unexpected mutation counts: create=%d update=%d delete=%d", createCalls, updateCalls, deleteCalls)
	}
}

func TestCloudflareDNSRecordResCheckApplyFailsSafelyWithoutPaginationMetadata(t *testing.T) {
	t.Parallel()

	fixture := newCloudflareDNSTestFixture(t)
	fixture.zones = []cloudflareZone{
		{ID: "zone-1", Name: "example.test"},
	}
	fixture.recordListIncludeResultInfo = false
	fixture.records["zone-1"] = make([]cloudflareDNSRecord, 0, 101)
	for i := 1; i <= 101; i++ {
		fixture.records["zone-1"] = append(fixture.records["zone-1"], cloudflareDNSRecord{
			ID:      fmt.Sprintf("record-%d", i),
			Type:    "TXT",
			Name:    "telemetry.edge.example.test",
			Content: fmt.Sprintf("value-%03d", i),
			TTL:     300,
		})
	}

	resource := newCloudflareDNSTestResource(fixture.server.URL)
	resource.ZoneName = "example.test"
	resource.State = CloudflareDNSStateAbsent
	resource.Type = "TXT"
	resource.RecordName = "telemetry.edge"
	resource.Content = "value-001"
	resource.TTL = 300

	if err := resource.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if err := resource.Init(nil); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	defer resource.Cleanup()

	checkOK, err := resource.CheckApply(context.Background(), true)
	if err == nil || !strings.Contains(err.Error(), "cannot safely reconcile incomplete record set") {
		t.Fatalf("expected pagination safety error, got: %v", err)
	}
	if checkOK {
		t.Fatalf("expected failed apply to report non-converged")
	}
	createCalls, updateCalls, deleteCalls := fixture.mutationCounts()
	if createCalls != 0 || updateCalls != 0 || deleteCalls != 0 {
		t.Fatalf("expected no mutations after pagination safety failure, got create=%d update=%d delete=%d", createCalls, updateCalls, deleteCalls)
	}
	if got := len(fixture.recordsForZone("zone-1")); got != 101 {
		t.Fatalf("expected records to remain untouched after pagination safety failure, got %d", got)
	}
}

func TestCloudflareDNSRecordResValidateRejectsDuplicateDesiredContent(t *testing.T) {
	t.Parallel()

	resource := newCloudflareDNSTestResource("https://example.invalid")
	resource.ZoneName = "example.test"
	resource.Type = "A"
	resource.RecordName = "cdn.edge"
	resource.Content = "198.51.100.10"
	resource.Contents = []string{"198.51.100.10"}

	err := resource.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate record content") {
		t.Fatalf("expected duplicate desired content error, got: %v", err)
	}
}

func TestCloudflareDNSRecordResValidateRejectsMultiValueCNAME(t *testing.T) {
	t.Parallel()

	resource := newCloudflareDNSTestResource("https://example.invalid")
	resource.ZoneName = "example.test"
	resource.Type = "CNAME"
	resource.RecordName = "api.edge"
	resource.Contents = []string{"origin-a.example.net", "origin-b.example.net"}

	err := resource.Validate()
	if err == nil || !strings.Contains(err.Error(), "cname record sets can only contain one value") {
		t.Fatalf("expected cname validation error, got: %v", err)
	}
}

func newCloudflareDNSTestResource(baseURL string) *CloudflareDNSRecordRes {
	resource := &CloudflareDNSRecordRes{
		APIToken: "test-token",
		State:    CloudflareDNSStateExists,
		TTL:      1,
		baseURL:  baseURL,
	}
	return resource
}

type cloudflareDNSTestFixture struct {
	t      *testing.T
	server *httptest.Server

	mu sync.Mutex

	nextID                      int
	zones                       []cloudflareZone
	records                     map[string][]cloudflareDNSRecord
	recordListIncludeResultInfo bool
	listedRecordPages           []int
	createCalls                 int
	updateCalls                 int
	deleteCalls                 int
}

func newCloudflareDNSTestFixture(t *testing.T) *cloudflareDNSTestFixture {
	t.Helper()

	fixture := &cloudflareDNSTestFixture{
		t: t,
		zones: []cloudflareZone{
			{ID: "zone-1", Name: "example.test"},
		},
		records:                     map[string][]cloudflareDNSRecord{},
		nextID:                      2,
		recordListIncludeResultInfo: true,
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	t.Cleanup(func() {
		fixture.server.Close()
	})
	return fixture
}

func (obj *cloudflareDNSTestFixture) recordsForZone(zoneID string) []cloudflareDNSRecord {
	obj.mu.Lock()
	defer obj.mu.Unlock()

	records := obj.records[zoneID]
	out := make([]cloudflareDNSRecord, len(records))
	copy(out, records)
	return out
}

func (obj *cloudflareDNSTestFixture) recordListPages() []int {
	obj.mu.Lock()
	defer obj.mu.Unlock()

	out := make([]int, len(obj.listedRecordPages))
	copy(out, obj.listedRecordPages)
	return out
}

func (obj *cloudflareDNSTestFixture) mutationCounts() (int, int, int) {
	obj.mu.Lock()
	defer obj.mu.Unlock()
	return obj.createCalls, obj.updateCalls, obj.deleteCalls
}

func cloudflareDNSTestContents(records []cloudflareDNSRecord) string {
	values := make([]string, 0, len(records))
	for _, record := range records {
		values = append(values, record.Content)
	}
	slices.Sort(values)
	return strings.Join(values, ",")
}

func (obj *cloudflareDNSTestFixture) serveHTTP(w http.ResponseWriter, req *http.Request) {
	obj.t.Helper()

	if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
		obj.t.Fatalf("unexpected authorization header: %s", got)
	}

	switch {
	case req.Method == http.MethodGet && req.URL.Path == "/zones":
		obj.handleListZones(w, req)
		return
	case strings.HasPrefix(req.URL.Path, "/zones/") && strings.Contains(req.URL.Path, "/dns_records"):
		obj.handleDNSRecords(w, req)
		return
	default:
		http.Error(w, fmt.Sprintf("unexpected request: %s %s", req.Method, req.URL.Path), http.StatusNotFound)
	}
}

func (obj *cloudflareDNSTestFixture) handleListZones(w http.ResponseWriter, req *http.Request) {
	obj.mu.Lock()
	defer obj.mu.Unlock()

	name := normalizeCloudflareDNSName(req.URL.Query().Get("name"))
	var result []cloudflareZone
	for _, zone := range obj.zones {
		if name == "" || normalizeCloudflareDNSName(zone.Name) == name {
			result = append(result, zone)
		}
	}
	obj.writeJSON(w, cloudflareEnvelope[[]cloudflareZone]{
		Success: true,
		Result:  result,
	})
}

func (obj *cloudflareDNSTestFixture) handleDNSRecords(w http.ResponseWriter, req *http.Request) {
	obj.mu.Lock()
	defer obj.mu.Unlock()

	path := strings.Trim(req.URL.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 || parts[0] != "zones" || parts[2] != "dns_records" {
		http.Error(w, "bad dns records path", http.StatusNotFound)
		return
	}
	zoneID := parts[1]

	switch req.Method {
	case http.MethodGet:
		obj.handleListRecords(w, req, zoneID)
	case http.MethodPost:
		obj.handleCreateRecord(w, req, zoneID)
	case http.MethodPut:
		if len(parts) != 4 {
			http.Error(w, "missing record id", http.StatusNotFound)
			return
		}
		obj.handleUpdateRecord(w, req, zoneID, parts[3])
	case http.MethodDelete:
		if len(parts) != 4 {
			http.Error(w, "missing record id", http.StatusNotFound)
			return
		}
		obj.handleDeleteRecord(w, zoneID, parts[3])
	default:
		http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
	}
}

func (obj *cloudflareDNSTestFixture) handleListRecords(w http.ResponseWriter, req *http.Request, zoneID string) {
	recordType := strings.ToUpper(req.URL.Query().Get("type"))
	name := normalizeCloudflareDNSName(req.URL.Query().Get("name"))

	var result []cloudflareDNSRecord
	for _, record := range obj.records[zoneID] {
		if recordType != "" && strings.ToUpper(record.Type) != recordType {
			continue
		}
		if name != "" && normalizeCloudflareDNSName(record.Name) != name {
			continue
		}
		result = append(result, record)
	}

	page := 1
	if raw := req.URL.Query().Get("page"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			obj.t.Fatalf("invalid page query %q: %v", raw, err)
		}
		if value > 0 {
			page = value
		}
	}
	perPage := len(result)
	if perPage == 0 {
		perPage = 1
	}
	if raw := req.URL.Query().Get("per_page"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			obj.t.Fatalf("invalid per_page query %q: %v", raw, err)
		}
		if value > 0 {
			perPage = value
		}
	}
	start := (page - 1) * perPage
	if start > len(result) {
		start = len(result)
	}
	end := start + perPage
	if end > len(result) {
		end = len(result)
	}
	pageResult := result[start:end]
	obj.listedRecordPages = append(obj.listedRecordPages, page)

	envelope := cloudflareEnvelope[[]cloudflareDNSRecord]{
		Success: true,
		Result:  pageResult,
	}
	if obj.recordListIncludeResultInfo {
		totalPages := 1
		if len(result) > 0 {
			totalPages = (len(result) + perPage - 1) / perPage
		}
		envelope.ResultInfo = &cloudflareResultInfo{
			Page:       page,
			PerPage:    perPage,
			Count:      len(pageResult),
			TotalCount: len(result),
			TotalPages: totalPages,
		}
	}
	obj.writeJSON(w, envelope)
}

func (obj *cloudflareDNSTestFixture) handleCreateRecord(w http.ResponseWriter, req *http.Request, zoneID string) {
	var body cloudflareDNSRecordRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		obj.t.Fatalf("decode create body failed: %v", err)
	}

	record := cloudflareDNSRecord{
		ID:        fmt.Sprintf("record-%d", obj.nextID),
		Type:      body.Type,
		Name:      body.Name,
		Content:   body.Content,
		TTL:       body.TTL,
		Comment:   body.Comment,
		Tags:      normalizeCloudflareDNSTags(body.Tags),
		Proxiable: cloudflareDNSRecordSupportsProxy(body.Type),
	}
	if body.Proxied != nil {
		record.Proxied = *body.Proxied
	}

	obj.nextID++
	obj.createCalls++
	obj.records[zoneID] = append(obj.records[zoneID], record)
	obj.writeJSON(w, cloudflareEnvelope[cloudflareDNSRecord]{
		Success: true,
		Result:  record,
	})
}

func (obj *cloudflareDNSTestFixture) handleUpdateRecord(w http.ResponseWriter, req *http.Request, zoneID, recordID string) {
	var body cloudflareDNSRecordRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		obj.t.Fatalf("decode update body failed: %v", err)
	}

	records := obj.records[zoneID]
	for i := range records {
		if records[i].ID != recordID {
			continue
		}
		obj.updateCalls++
		records[i].Type = body.Type
		records[i].Name = body.Name
		records[i].Content = body.Content
		records[i].TTL = body.TTL
		records[i].Comment = body.Comment
		records[i].Tags = normalizeCloudflareDNSTags(body.Tags)
		records[i].Proxiable = cloudflareDNSRecordSupportsProxy(body.Type)
		records[i].Proxied = false
		if body.Proxied != nil {
			records[i].Proxied = *body.Proxied
		}
		obj.records[zoneID] = records
		obj.writeJSON(w, cloudflareEnvelope[cloudflareDNSRecord]{
			Success: true,
			Result:  records[i],
		})
		return
	}

	obj.writeJSONStatus(w, http.StatusNotFound, cloudflareEnvelope[cloudflareDNSRecord]{
		Success: false,
		Errors: []cloudflareAPIError{
			{Code: 81044, Message: "record not found"},
		},
	})
}

func (obj *cloudflareDNSTestFixture) handleDeleteRecord(w http.ResponseWriter, zoneID, recordID string) {
	records := obj.records[zoneID]
	for i := range records {
		if records[i].ID != recordID {
			continue
		}
		obj.deleteCalls++
		obj.records[zoneID] = append(records[:i], records[i+1:]...)
		obj.writeJSON(w, cloudflareEnvelope[cloudflareDeleteResult]{
			Success: true,
			Result:  cloudflareDeleteResult{ID: recordID},
		})
		return
	}

	obj.writeJSONStatus(w, http.StatusNotFound, cloudflareEnvelope[cloudflareDeleteResult]{
		Success: false,
		Errors: []cloudflareAPIError{
			{Code: 81044, Message: "record not found"},
		},
	})
}

func (obj *cloudflareDNSTestFixture) writeJSON(w http.ResponseWriter, value any) {
	obj.writeJSONStatus(w, http.StatusOK, value)
}

func (obj *cloudflareDNSTestFixture) writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		obj.t.Fatalf("encode response failed: %v", err)
	}
}
