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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
	"github.com/purpleidea/mgmt/util/errwrap"
)

func init() {
	engine.RegisterResource("cloudflare:dns_record", func() engine.Res { return &CloudflareDNSRecordRes{} })
}

const (
	CloudflareDNSStateExists = "exists"
	CloudflareDNSStateAbsent = "absent"

	cloudflareDNSAPIBaseURL = "https://api.cloudflare.com/client/v4"
	cloudflareDNSPollLimit  = 5
)

var cloudflareDNSRecordTypes = map[string]struct{}{
	"A":     {},
	"AAAA":  {},
	"CNAME": {},
	"TXT":   {},
}

// CloudflareDNSRecordRes manages the full DNS record set for one (zone, type,
// name) tuple inside a Cloudflare zone. The legacy Content field continues to
// support singleton record sets, while Contents lets callers manage multiple
// same-name records declaratively.
type CloudflareDNSRecordRes struct {
	traits.Base

	init *engine.Init

	APIToken   string   `lang:"apitoken"`
	State      string   `lang:"state"`
	ZoneID     string   `lang:"zoneid"`
	ZoneName   string   `lang:"zonename"`
	Type       string   `lang:"type"`
	RecordName string   `lang:"name"`
	Content    string   `lang:"content"`
	Contents   []string `lang:"contents"`
	TTL        int      `lang:"ttl"`
	Proxied    bool     `lang:"proxied"`
	Comment    string   `lang:"comment"`
	Tags       []string `lang:"tags"`

	client  *http.Client
	baseURL string
}

// Default returns conservative defaults for this resource.
func (obj *CloudflareDNSRecordRes) Default() engine.Res {
	return &CloudflareDNSRecordRes{
		State: CloudflareDNSStateExists,
		TTL:   1,
	}
}

// Validate checks whether the requested record spec is valid.
func (obj *CloudflareDNSRecordRes) Validate() error {
	if obj.APIToken == "" {
		return fmt.Errorf("empty token string")
	}
	if err := validateCloudflareDNSState(obj.State); err != nil {
		return err
	}
	if obj.ZoneID == "" && obj.ZoneName == "" {
		return fmt.Errorf("one of zoneid or zonename must be specified")
	}
	if err := validateCloudflareDNSType(obj.Type); err != nil {
		return err
	}
	if strings.TrimSpace(obj.RecordName) == "" {
		return fmt.Errorf("empty record name")
	}
	if _, err := obj.desiredRecordSetSpec(); err != nil {
		return err
	}
	if obj.TTL != 1 && (obj.TTL < 60 || obj.TTL > 86400) {
		return fmt.Errorf("invalid ttl: must be 1 or between 60 and 86400")
	}
	if obj.MetaParams().Poll > 0 && obj.MetaParams().Poll < cloudflareDNSPollLimit {
		return fmt.Errorf("invalid polling interval (minimum %d s)", cloudflareDNSPollLimit)
	}
	return nil
}

// Init runs startup code for this resource.
func (obj *CloudflareDNSRecordRes) Init(init *engine.Init) error {
	obj.init = init
	obj.client = &http.Client{}
	if obj.baseURL == "" {
		obj.baseURL = cloudflareDNSAPIBaseURL
	}
	return nil
}

// Cleanup removes authentication and client state from memory.
func (obj *CloudflareDNSRecordRes) Cleanup() error {
	obj.APIToken = ""
	if obj.client != nil {
		obj.client.CloseIdleConnections()
	}
	obj.client = nil
	obj.baseURL = ""
	return nil
}

// Watch is not implemented for this resource, since the Cloudflare DNS API
// does not provide native event streaming.
func (obj *CloudflareDNSRecordRes) Watch(context.Context) error {
	return fmt.Errorf("invalid Watch call: requires poll metaparam")
}

// CheckApply checks and applies DNS record lifecycle state.
func (obj *CloudflareDNSRecordRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	recordSet, err := obj.desiredRecordSetSpec()
	if err != nil {
		return false, err
	}

	zoneID, err := obj.resolveZoneID(ctx)
	if err != nil {
		return false, errwrap.Wrapf(err, "resolveZoneID failed")
	}

	records, err := obj.lookupRecords(ctx, zoneID, recordSet.Name, recordSet.Type)
	if err != nil {
		return false, errwrap.Wrapf(err, "lookupRecords failed")
	}

	if obj.State == CloudflareDNSStateAbsent {
		if len(records) == 0 {
			return true, nil
		}
		if !apply {
			return false, nil
		}
		for _, record := range records {
			if err := obj.deleteRecord(ctx, zoneID, record.ID); err != nil {
				return false, errwrap.Wrapf(err, "deleteRecord failed")
			}
		}
		return false, nil
	}

	checkOK, plan := cloudflareDNSBuildPlan(records, recordSet)
	if checkOK {
		return true, nil
	}
	if !apply {
		return false, nil
	}

	for _, update := range plan.Updates {
		if err := obj.updateRecord(ctx, zoneID, update.ID, update.Record); err != nil {
			return false, errwrap.Wrapf(err, "updateRecord failed")
		}
	}
	for _, create := range plan.Creates {
		if err := obj.createRecord(ctx, zoneID, create); err != nil {
			return false, errwrap.Wrapf(err, "createRecord failed")
		}
	}
	for _, deleteID := range plan.Deletes {
		if err := obj.deleteRecord(ctx, zoneID, deleteID); err != nil {
			return false, errwrap.Wrapf(err, "deleteRecord failed")
		}
	}
	return false, nil
}

func (obj *CloudflareDNSRecordRes) desiredRecordSetSpec() (*cloudflareDNSRecordSetSpec, error) {
	zoneName := normalizeCloudflareDNSName(obj.ZoneName)
	name, err := normalizeCloudflareDNSRecordName(obj.RecordName, zoneName)
	if err != nil {
		return nil, err
	}
	contents, err := normalizeCloudflareDNSDesiredContents(obj.Type, obj.Content, obj.Contents)
	if err != nil {
		return nil, err
	}

	recordSet := &cloudflareDNSRecordSetSpec{
		Type:     strings.ToUpper(obj.Type),
		Name:     name,
		Contents: contents,
		TTL:      obj.TTL,
		Comment:  obj.Comment,
		Tags:     normalizeCloudflareDNSTags(obj.Tags),
	}
	if obj.State == CloudflareDNSStateExists && len(recordSet.Contents) == 0 {
		return nil, fmt.Errorf("record set requires content or contents")
	}
	if recordSet.Type == "CNAME" && len(recordSet.Contents) > 1 {
		return nil, fmt.Errorf("cname record sets can only contain one value")
	}
	if cloudflareDNSRecordSupportsProxy(recordSet.Type) {
		proxied := obj.Proxied
		recordSet.Proxied = &proxied
	}
	return recordSet, nil
}

func (obj *CloudflareDNSRecordRes) resolveZoneID(ctx context.Context) (string, error) {
	if obj.ZoneID != "" {
		return obj.ZoneID, nil
	}

	zoneName := normalizeCloudflareDNSName(obj.ZoneName)
	values := url.Values{}
	values.Set("name", zoneName)
	values.Set("match", "all")
	values.Set("per_page", "50")

	var envelope cloudflareEnvelope[[]cloudflareZone]
	if err := obj.doJSON(ctx, http.MethodGet, "/zones?"+values.Encode(), nil, &envelope); err != nil {
		return "", err
	}

	exact := make([]cloudflareZone, 0, len(envelope.Result))
	for _, zone := range envelope.Result {
		if normalizeCloudflareDNSName(zone.Name) == zoneName {
			exact = append(exact, zone)
		}
	}
	if len(exact) == 0 {
		return "", fmt.Errorf("zone not found: %s", zoneName)
	}
	if len(exact) > 1 {
		return "", fmt.Errorf("multiple zones matched: %s", zoneName)
	}
	return exact[0].ID, nil
}

func (obj *CloudflareDNSRecordRes) lookupRecords(ctx context.Context, zoneID, name, recordType string) ([]cloudflareDNSRecord, error) {
	values := url.Values{}
	values.Set("type", recordType)
	values.Set("name", name)
	values.Set("per_page", "100")

	var envelope cloudflareEnvelope[[]cloudflareDNSRecord]
	if err := obj.doJSON(ctx, http.MethodGet, fmt.Sprintf("/zones/%s/dns_records?%s", zoneID, values.Encode()), nil, &envelope); err != nil {
		return nil, err
	}

	matches := make([]cloudflareDNSRecord, 0, len(envelope.Result))
	for _, record := range envelope.Result {
		if strings.EqualFold(record.Type, recordType) && normalizeCloudflareDNSName(record.Name) == normalizeCloudflareDNSName(name) {
			record.Name = normalizeCloudflareDNSName(record.Name)
			record.Content = normalizeCloudflareDNSExistingContent(record.Type, record.Content)
			record.Tags = normalizeCloudflareDNSTags(record.Tags)
			matches = append(matches, record)
		}
	}
	slices.SortFunc(matches, func(a, b cloudflareDNSRecord) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return matches, nil
}

func (obj *CloudflareDNSRecordRes) createRecord(ctx context.Context, zoneID string, args *cloudflareDNSRecordRequest) error {
	var envelope cloudflareEnvelope[cloudflareDNSRecord]
	return obj.doJSON(ctx, http.MethodPost, fmt.Sprintf("/zones/%s/dns_records", zoneID), args, &envelope)
}

func (obj *CloudflareDNSRecordRes) updateRecord(ctx context.Context, zoneID, recordID string, args *cloudflareDNSRecordRequest) error {
	var envelope cloudflareEnvelope[cloudflareDNSRecord]
	return obj.doJSON(ctx, http.MethodPut, fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, recordID), args, &envelope)
}

func (obj *CloudflareDNSRecordRes) deleteRecord(ctx context.Context, zoneID, recordID string) error {
	var envelope cloudflareEnvelope[cloudflareDeleteResult]
	return obj.doJSON(ctx, http.MethodDelete, fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, recordID), nil, &envelope)
}

func (obj *CloudflareDNSRecordRes) doJSON(ctx context.Context, method, path string, reqBody, respBody any) error {
	endpoint := strings.TrimRight(obj.baseURL, "/") + path

	var body io.Reader
	if reqBody != nil {
		payload, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+obj.APIToken)
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := obj.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if len(payload) == 0 {
		payload = []byte(`{"success":true,"result":null}`)
	}
	if err := json.Unmarshal(payload, respBody); err != nil {
		return errwrap.Wrapf(err, "decode response failed: status=%d body=%s", resp.StatusCode, string(payload))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if msg := cloudflareEnvelopeError(respBody); msg != "" {
			return fmt.Errorf("cloudflare api error: %s", msg)
		}
		return fmt.Errorf("cloudflare api returned status %d", resp.StatusCode)
	}
	if msg := cloudflareEnvelopeError(respBody); msg != "" {
		return fmt.Errorf("cloudflare api error: %s", msg)
	}
	return nil
}

// Cmp compares two resources and returns an error if they are not equivalent.
func (obj *CloudflareDNSRecordRes) Cmp(r engine.Res) error {
	res, ok := r.(*CloudflareDNSRecordRes)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}

	if obj.APIToken != res.APIToken {
		return fmt.Errorf("apitoken differs")
	}
	if obj.State != res.State {
		return fmt.Errorf("state differs")
	}
	if obj.ZoneID != res.ZoneID {
		return fmt.Errorf("zoneid differs")
	}
	if obj.ZoneName != res.ZoneName {
		return fmt.Errorf("zonename differs")
	}
	if obj.Type != res.Type {
		return fmt.Errorf("type differs")
	}
	if obj.RecordName != res.RecordName {
		return fmt.Errorf("name differs")
	}
	if obj.Content != res.Content {
		return fmt.Errorf("content differs")
	}
	if !slices.Equal(normalizeCloudflareDNSDesiredContentsForCmp(obj.Type, obj.Contents), normalizeCloudflareDNSDesiredContentsForCmp(res.Type, res.Contents)) {
		return fmt.Errorf("contents differ")
	}
	if obj.TTL != res.TTL {
		return fmt.Errorf("ttl differs")
	}
	if obj.Proxied != res.Proxied {
		return fmt.Errorf("proxied differs")
	}
	if obj.Comment != res.Comment {
		return fmt.Errorf("comment differs")
	}
	if !slices.Equal(normalizeCloudflareDNSTags(obj.Tags), normalizeCloudflareDNSTags(res.Tags)) {
		return fmt.Errorf("tags differ")
	}
	return nil
}

// CloudflareDNSRecordUID is the UID struct for CloudflareDNSRecordRes.
type CloudflareDNSRecordUID struct {
	engine.BaseUID

	name string
}

// UIDs includes all params to make a unique identification of this object.
func (obj *CloudflareDNSRecordRes) UIDs() []engine.ResUID {
	x := &CloudflareDNSRecordUID{
		BaseUID: engine.BaseUID{Name: obj.Name(), Kind: obj.Kind()},
		name:    obj.Name(),
	}
	return []engine.ResUID{x}
}

func validateCloudflareDNSState(state string) error {
	switch state {
	case CloudflareDNSStateExists, CloudflareDNSStateAbsent:
		return nil
	default:
		return fmt.Errorf("invalid state: %s", state)
	}
}

func validateCloudflareDNSType(recordType string) error {
	recordType = strings.ToUpper(recordType)
	if _, exists := cloudflareDNSRecordTypes[recordType]; !exists {
		return fmt.Errorf("unsupported dns record type: %s", recordType)
	}
	return nil
}

func normalizeCloudflareDNSContent(recordType, content string) (string, error) {
	recordType = strings.ToUpper(recordType)
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("empty record content")
	}

	switch recordType {
	case "A":
		ip := net.ParseIP(content)
		if ip == nil || ip.To4() == nil {
			return "", fmt.Errorf("invalid ipv4 address: %s", content)
		}
		return ip.To4().String(), nil
	case "AAAA":
		ip := net.ParseIP(content)
		if ip == nil || ip.To4() != nil {
			return "", fmt.Errorf("invalid ipv6 address: %s", content)
		}
		return ip.String(), nil
	case "CNAME":
		return normalizeCloudflareDNSName(content), nil
	case "TXT":
		return content, nil
	default:
		return "", fmt.Errorf("unsupported dns record type: %s", recordType)
	}
}

func normalizeCloudflareDNSDesiredContents(recordType, content string, contents []string) ([]string, error) {
	recordType = strings.ToUpper(recordType)
	normalized := make([]string, 0, len(contents)+1)
	seen := make(map[string]struct{}, len(contents)+1)

	appendContent := func(value string) error {
		if strings.TrimSpace(value) == "" {
			return nil
		}
		normalizedValue, err := normalizeCloudflareDNSContent(recordType, value)
		if err != nil {
			return err
		}
		if _, exists := seen[normalizedValue]; exists {
			return fmt.Errorf("duplicate record content: %s", normalizedValue)
		}
		seen[normalizedValue] = struct{}{}
		normalized = append(normalized, normalizedValue)
		return nil
	}

	if err := appendContent(content); err != nil {
		return nil, err
	}
	for _, value := range contents {
		if err := appendContent(value); err != nil {
			return nil, err
		}
	}

	slices.Sort(normalized)
	return normalized, nil
}

func normalizeCloudflareDNSDesiredContentsForCmp(recordType string, contents []string) []string {
	normalized, err := normalizeCloudflareDNSDesiredContents(recordType, "", contents)
	if err != nil {
		return contents
	}
	return normalized
}

func normalizeCloudflareDNSExistingContent(recordType, content string) string {
	normalized, err := normalizeCloudflareDNSContent(recordType, content)
	if err != nil {
		return content
	}
	return normalized
}

func normalizeCloudflareDNSName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

func normalizeCloudflareDNSRecordName(name, zoneName string) (string, error) {
	name = normalizeCloudflareDNSName(name)
	zoneName = normalizeCloudflareDNSName(zoneName)
	if name == "" {
		return "", fmt.Errorf("empty record name")
	}
	if name == "@" {
		if zoneName == "" {
			return "", fmt.Errorf("record name '@' requires zonename")
		}
		return zoneName, nil
	}
	if zoneName == "" {
		return name, nil
	}
	if name == zoneName || strings.HasSuffix(name, "."+zoneName) {
		return name, nil
	}
	return name + "." + zoneName, nil
}

func normalizeCloudflareDNSTags(tags []string) []string {
	if len(tags) == 0 {
		return []string{}
	}
	normalized := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		normalized = append(normalized, tag)
	}
	slices.Sort(normalized)
	return normalized
}

func cloudflareDNSRecordSupportsProxy(recordType string) bool {
	switch strings.ToUpper(recordType) {
	case "A", "AAAA", "CNAME":
		return true
	default:
		return false
	}
}

func cloudflareDNSRecordMatches(record *cloudflareDNSRecord, args *cloudflareDNSRecordRequest) bool {
	if record == nil || args == nil {
		return false
	}
	if !strings.EqualFold(record.Type, args.Type) {
		return false
	}
	if normalizeCloudflareDNSName(record.Name) != normalizeCloudflareDNSName(args.Name) {
		return false
	}
	if normalizeCloudflareDNSExistingContent(record.Type, record.Content) != args.Content {
		return false
	}
	if record.TTL != args.TTL {
		return false
	}
	if record.Comment != args.Comment {
		return false
	}
	if !slices.Equal(normalizeCloudflareDNSTags(record.Tags), args.Tags) {
		return false
	}
	if args.Proxied != nil {
		if record.Proxied != *args.Proxied {
			return false
		}
	}
	return true
}

func cloudflareDNSBuildPlan(records []cloudflareDNSRecord, desired *cloudflareDNSRecordSetSpec) (bool, *cloudflareDNSRecordPlan) {
	plan := &cloudflareDNSRecordPlan{}
	if desired == nil {
		for _, record := range records {
			plan.Deletes = append(plan.Deletes, record.ID)
		}
		return len(plan.Deletes) == 0, plan
	}

	used := make([]bool, len(records))
	for _, content := range desired.Contents {
		args := desired.request(content)

		exactIdx := cloudflareDNSFindUnusedRecordByContent(records, used, content)
		if exactIdx >= 0 {
			used[exactIdx] = true
			if !cloudflareDNSRecordMatches(&records[exactIdx], args) {
				plan.Updates = append(plan.Updates, cloudflareDNSRecordUpdate{
					ID:     records[exactIdx].ID,
					Record: args,
				})
			}
			continue
		}

		reuseIdx := cloudflareDNSFindFirstUnusedRecord(records, used)
		if reuseIdx >= 0 {
			used[reuseIdx] = true
			plan.Updates = append(plan.Updates, cloudflareDNSRecordUpdate{
				ID:     records[reuseIdx].ID,
				Record: args,
			})
			continue
		}

		plan.Creates = append(plan.Creates, args)
	}

	for idx, record := range records {
		if used[idx] {
			continue
		}
		plan.Deletes = append(plan.Deletes, record.ID)
	}

	checkOK := len(plan.Creates) == 0 && len(plan.Updates) == 0 && len(plan.Deletes) == 0
	return checkOK, plan
}

func cloudflareDNSFindUnusedRecordByContent(records []cloudflareDNSRecord, used []bool, content string) int {
	for idx, record := range records {
		if used[idx] {
			continue
		}
		if record.Content == content {
			return idx
		}
	}
	return -1
}

func cloudflareDNSFindFirstUnusedRecord(records []cloudflareDNSRecord, used []bool) int {
	for idx := range records {
		if !used[idx] {
			return idx
		}
	}
	return -1
}

func cloudflareEnvelopeError(value any) string {
	switch env := value.(type) {
	case *cloudflareEnvelope[[]cloudflareZone]:
		return env.errorString()
	case *cloudflareEnvelope[[]cloudflareDNSRecord]:
		return env.errorString()
	case *cloudflareEnvelope[cloudflareDNSRecord]:
		return env.errorString()
	case *cloudflareEnvelope[cloudflareDeleteResult]:
		return env.errorString()
	default:
		return ""
	}
}

type cloudflareEnvelope[T any] struct {
	Success  bool                   `json:"success"`
	Errors   []cloudflareAPIError   `json:"errors"`
	Messages []cloudflareAPIMessage `json:"messages"`
	Result   T                      `json:"result"`
}

func (obj *cloudflareEnvelope[T]) errorString() string {
	if obj == nil {
		return ""
	}
	if obj.Success && len(obj.Errors) == 0 {
		return ""
	}
	if len(obj.Errors) == 0 {
		return "request unsuccessful"
	}
	parts := make([]string, 0, len(obj.Errors))
	for _, err := range obj.Errors {
		if err.Code != 0 {
			parts = append(parts, fmt.Sprintf("%d: %s", err.Code, err.Message))
			continue
		}
		parts = append(parts, err.Message)
	}
	return strings.Join(parts, "; ")
}

type cloudflareAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cloudflareAPIMessage struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cloudflareZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cloudflareDNSRecord struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	Name      string   `json:"name"`
	Content   string   `json:"content"`
	TTL       int      `json:"ttl"`
	Proxied   bool     `json:"proxied"`
	Comment   string   `json:"comment"`
	Tags      []string `json:"tags"`
	Proxiable bool     `json:"proxiable"`
}

type cloudflareDNSRecordRequest struct {
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Content string   `json:"content"`
	TTL     int      `json:"ttl"`
	Proxied *bool    `json:"proxied,omitempty"`
	Comment string   `json:"comment"`
	Tags    []string `json:"tags"`
}

type cloudflareDeleteResult struct {
	ID string `json:"id"`
}

type cloudflareDNSRecordSetSpec struct {
	Type     string
	Name     string
	Contents []string
	TTL      int
	Proxied  *bool
	Comment  string
	Tags     []string
}

func (obj *cloudflareDNSRecordSetSpec) request(content string) *cloudflareDNSRecordRequest {
	return &cloudflareDNSRecordRequest{
		Type:    obj.Type,
		Name:    obj.Name,
		Content: content,
		TTL:     obj.TTL,
		Proxied: obj.Proxied,
		Comment: obj.Comment,
		Tags:    obj.Tags,
	}
}

type cloudflareDNSRecordPlan struct {
	Creates []*cloudflareDNSRecordRequest
	Updates []cloudflareDNSRecordUpdate
	Deletes []string
}

type cloudflareDNSRecordUpdate struct {
	ID     string
	Record *cloudflareDNSRecordRequest
}
