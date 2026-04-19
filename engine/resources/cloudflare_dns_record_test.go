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
	"strings"
	"sync"
	"testing"
)

func TestCloudflareDNSRecordResCheckApplyCreatesRecord(t *testing.T) {
	t.Parallel()

	fixture := newCloudflareDNSTestFixture(t)
	resource := newCloudflareDNSTestResource(fixture.server.URL)
	resource.ZoneName = "fishystuff.fish"
	resource.Type = "CNAME"
	resource.RecordName = "api.beta"
	resource.Content = "beta-nbg1-api-db.example.net"
	resource.TTL = 300
	resource.Proxied = true
	resource.Comment = "managed by mgmt"
	resource.Tags = []string{"role:api", "env:beta"}

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
	if record.Name != "api.beta.fishystuff.fish" {
		t.Fatalf("unexpected record name: %s", record.Name)
	}
	if record.Content != "beta-nbg1-api-db.example.net" {
		t.Fatalf("unexpected record content: %s", record.Content)
	}
	if !record.Proxied {
		t.Fatalf("expected proxied record")
	}
	if got := strings.Join(record.Tags, ","); got != "env:beta,role:api" {
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
			Name:      "api.beta.fishystuff.fish",
			Content:   "203.0.113.10",
			TTL:       120,
			Proxied:   false,
			Comment:   "old",
			Tags:      []string{"env:beta"},
			Proxiable: true,
		},
	}

	resource := newCloudflareDNSTestResource(fixture.server.URL)
	resource.ZoneName = "fishystuff.fish"
	resource.Type = "A"
	resource.RecordName = "api.beta"
	resource.Content = "203.0.113.22"
	resource.TTL = 300
	resource.Proxied = true
	resource.Comment = "managed by mgmt"
	resource.Tags = []string{"role:api", "env:beta"}

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
			Name:    "telemetry.beta.fishystuff.fish",
			Content: "hello",
			TTL:     300,
		},
		{
			ID:      "record-2",
			Type:    "TXT",
			Name:    "telemetry.beta.fishystuff.fish",
			Content: "world",
			TTL:     300,
		},
	}

	resource := newCloudflareDNSTestResource(fixture.server.URL)
	resource.ZoneName = "fishystuff.fish"
	resource.State = CloudflareDNSStateAbsent
	resource.Type = "TXT"
	resource.RecordName = "telemetry.beta"
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
	resource.ZoneName = "fishystuff.fish"
	resource.Type = "A"
	resource.RecordName = "cdn.beta"
	resource.Contents = []string{"198.51.100.10", "198.51.100.11"}
	resource.TTL = 300
	resource.Proxied = true
	resource.Comment = "managed by mgmt"
	resource.Tags = []string{"service:cdn", "env:beta"}

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

func TestCloudflareDNSRecordResCheckApplyReconcilesRecordSet(t *testing.T) {
	t.Parallel()

	fixture := newCloudflareDNSTestFixture(t)
	fixture.records["zone-1"] = []cloudflareDNSRecord{
		{
			ID:        "record-1",
			Type:      "A",
			Name:      "cdn.beta.fishystuff.fish",
			Content:   "198.51.100.10",
			TTL:       120,
			Proxied:   false,
			Comment:   "old",
			Tags:      []string{"env:beta"},
			Proxiable: true,
		},
		{
			ID:        "record-2",
			Type:      "A",
			Name:      "cdn.beta.fishystuff.fish",
			Content:   "198.51.100.12",
			TTL:       120,
			Proxied:   false,
			Comment:   "old",
			Tags:      []string{"env:beta"},
			Proxiable: true,
		},
	}

	resource := newCloudflareDNSTestResource(fixture.server.URL)
	resource.ZoneName = "fishystuff.fish"
	resource.Type = "A"
	resource.RecordName = "cdn.beta"
	resource.Contents = []string{"198.51.100.10", "198.51.100.11"}
	resource.TTL = 300
	resource.Proxied = true
	resource.Comment = "managed by mgmt"
	resource.Tags = []string{"service:cdn", "env:beta"}

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

func TestCloudflareDNSRecordResValidateRejectsDuplicateDesiredContent(t *testing.T) {
	t.Parallel()

	resource := newCloudflareDNSTestResource("https://example.invalid")
	resource.ZoneName = "fishystuff.fish"
	resource.Type = "A"
	resource.RecordName = "cdn.beta"
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
	resource.ZoneName = "fishystuff.fish"
	resource.Type = "CNAME"
	resource.RecordName = "api.beta"
	resource.Contents = []string{"one.example.net", "two.example.net"}

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

	mu      sync.Mutex
	nextID  int
	zones   []cloudflareZone
	records map[string][]cloudflareDNSRecord
}

func newCloudflareDNSTestFixture(t *testing.T) *cloudflareDNSTestFixture {
	t.Helper()

	fixture := &cloudflareDNSTestFixture{
		t: t,
		zones: []cloudflareZone{
			{ID: "zone-1", Name: "fishystuff.fish"},
		},
		records: map[string][]cloudflareDNSRecord{},
		nextID:  2,
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
	obj.writeJSON(w, cloudflareEnvelope[[]cloudflareDNSRecord]{
		Success: true,
		Result:  result,
	})
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
