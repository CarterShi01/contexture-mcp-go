package managed

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/CarterShi01/contexture-mcp-go/core/graph"
)

// CursorSigner owns a defensive copy of an explicit Host-owned key. Tokens are
// random/deterministic keyed opaque handles, never signed plaintext coordinates.
type CursorSigner struct {
	mu      sync.RWMutex
	key     []byte
	version string
}

func NewCursorSigner(key []byte) (*CursorSigner, error) {
	s := &CursorSigner{}
	if err := s.Rotate(key); err != nil {
		return nil, err
	}
	return s, nil
}
func randomReadID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func (s *CursorSigner) Rotate(key []byte) error {
	if len(key) < 32 {
		return errors.New("explicit Host-owned cursor key of at least 32 bytes required")
	}
	version, err := randomReadID()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.key, s.version = slices.Clone(key), version
	return nil
}
func (s *CursorSigner) Version() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}
func (s *CursorSigner) issue(subject string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.key) < 32 {
		return "", readError("contract_mismatch")
	}
	handle := ""
	if subject == "" {
		var err error
		handle, err = randomReadID()
		if err != nil {
			return "", err
		}
	} else {
		h := hmac.New(sha256.New, s.key)
		_, _ = h.Write([]byte(subject))
		handle = hex.EncodeToString(h.Sum(nil))
	}
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte("native-paging-v1\x00" + handle))
	return handle + "." + hex.EncodeToString(mac.Sum(nil)), nil
}
func (s *CursorSigner) valid(token string) bool {
	if len(token) != 129 || token[64] != '.' || strings.ToLower(token) != token {
		return false
	}
	if _, err := hex.DecodeString(token[:64]); err != nil {
		return false
	}
	signature, err := hex.DecodeString(token[65:])
	if err != nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.key) < 32 {
		return false
	}
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte("native-paging-v1\x00" + token[:64]))
	return hmac.Equal(signature, mac.Sum(nil))
}

type PageProfileOptions struct {
	Limit int
	Kinds []string
	Name  string
}
type PageProfile struct {
	Limit int
	Kinds []string
	Name  string
}
type CatalogProfile PageProfile

func normalizePageProfile(p PageProfile, name string) (PageProfile, error) {
	if p.Limit == 0 {
		p.Limit = 200
	}
	if p.Name == "" {
		p.Name = name
	}
	if p.Kinds == nil {
		p.Kinds = []string{"role", "skill", "tool"}
	} else {
		p.Kinds = slices.Clone(p.Kinds)
	}
	if p.Limit < 1 || p.Limit > 1000 || len(p.Name) > 128 {
		return PageProfile{}, readError("schema_invalid")
	}
	for _, kind := range p.Kinds {
		if kind != "role" && kind != "skill" && kind != "tool" {
			return PageProfile{}, readError("schema_invalid")
		}
	}
	slices.Sort(p.Kinds)
	p.Kinds = slices.Compact(p.Kinds)
	return p, nil
}
func NewPageProfile(o PageProfileOptions) (PageProfile, error) {
	return normalizePageProfile(PageProfile(o), "native-route-v1")
}
func NewCatalogProfile(o PageProfileOptions) (CatalogProfile, error) {
	p, err := normalizePageProfile(PageProfile(o), "native-metadata-catalog-v1")
	return CatalogProfile(p), err
}
func stringReadArray(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}
func nullableReadArray(values []string) any {
	if values == nil {
		return nil
	}
	return stringReadArray(values)
}
func (p PageProfile) asJSON() map[string]any {
	return map[string]any{"limit": p.Limit, "kinds": stringReadArray(p.Kinds), "name": p.Name}
}
func readEncode(value any) (string, error) {
	b, err := graph.CanonicalJSON(value)
	return string(b), err
}
func sameReadJSON(a, b FrozenJSON) bool {
	left, le := a.Canonical()
	right, re := b.Canonical()
	return le == nil && re == nil && bytes.Equal(left, right)
}
func readObject(value FrozenJSON) (map[string]any, error) {
	obj, ok := value.JSON().(map[string]any)
	if !ok {
		return nil, readError("contract_mismatch")
	}
	return obj, nil
}
func readDigest(value any) (string, error) {
	return graph.CanonicalDigest(value, "contexture.view/v1")
}

type PageStoreOptions struct {
	Signer        *CursorSigner
	MaxSnapshots  int
	MaxBytes      int
	MaxItems      int
	MaxCursors    int
	ResponseBytes int
	MaxTTL        time.Duration
	// Monotonic custom clocks require explicit Sweep.
	Clock func() time.Duration
}
type ReadCounts struct {
	Snapshots, Pending, Bytes, Cursors int
}
type readItem struct {
	value FrozenJSON
	size  int
}
type readStream struct {
	name  string
	items []readItem
}
type readSnapshot struct {
	id, binding, version string
	envelope             FrozenJSON
	streams              []readStream
	profile              PageProfile
	relationPages        bool
	expires              time.Duration
	size                 int
	authorize            func(context.Context, func() (FrozenJSON, error)) (FrozenJSON, error)
	dispose              func()
	cursors              map[readPosition]string
	timer                *time.Timer
}
type readPosition struct {
	stream string
	offset int
}
type readToken struct {
	snapshot *readSnapshot
	position readPosition
}
type snapshotPages struct {
	mu        sync.Mutex
	options   PageStoreOptions
	automatic bool
	snapshots map[string]*readSnapshot
	tokens    map[string]readToken
	bytes     int
	pending   int
	closed    bool
}

func readBound(value, fallback int) (int, error) {
	if value == 0 {
		value = fallback
	}
	if value < 1 {
		return 0, readError("schema_invalid")
	}
	return value, nil
}
func newSnapshotPages(o PageStoreOptions) (*snapshotPages, error) {
	if o.Signer == nil || o.Signer.Version() == "" {
		return nil, errors.New("CursorSigner required")
	}
	for _, item := range []struct {
		value    *int
		fallback int
	}{{&o.MaxSnapshots, 64}, {&o.MaxBytes, 16 << 20}, {&o.MaxItems, 10000}, {&o.MaxCursors, 20000}, {&o.ResponseBytes, 8 << 20}} {
		v, err := readBound(*item.value, item.fallback)
		if err != nil {
			return nil, err
		}
		*item.value = v
	}
	if o.MaxTTL == 0 {
		o.MaxTTL = time.Minute
	}
	if o.MaxTTL < 0 || o.MaxTTL > time.Hour {
		return nil, readError("schema_invalid")
	}
	automatic := o.Clock == nil
	if automatic {
		start := time.Now()
		o.Clock = func() time.Duration { return time.Since(start) }
	}
	return &snapshotPages{options: o, automatic: automatic, snapshots: map[string]*readSnapshot{}, tokens: map[string]readToken{}}, nil
}
func (p *snapshotPages) counts() ReadCounts {
	p.mu.Lock()
	defer p.mu.Unlock()
	return ReadCounts{len(p.snapshots), p.pending, p.bytes, len(p.tokens)}
}
func (p *snapshotPages) alive(s *readSnapshot) error {
	if p.closed || p.options.Clock() >= s.expires || s.version != p.options.Signer.Version() {
		return readError("resync_required")
	}
	return nil
}
func (p *snapshotPages) drop(s *readSnapshot) {
	if p.snapshots[s.id] != s {
		return
	}
	delete(p.snapshots, s.id)
	for _, token := range s.cursors {
		delete(p.tokens, token)
	}
	p.bytes -= s.size
	if s.timer != nil {
		s.timer.Stop()
	}
	s.dispose()
}
func (p *snapshotPages) discard(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s := p.snapshots[id]; s != nil {
		p.drop(s)
	}
}
func (p *snapshotPages) sweepLocked() int {
	removed := 0
	for _, s := range p.snapshots {
		if p.alive(s) != nil {
			p.drop(s)
			removed++
		}
	}
	return removed
}
func (p *snapshotPages) sweep() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sweepLocked()
}
func (p *snapshotPages) shutdown() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.sweepLocked()
}
func (p *snapshotPages) begin(ttl time.Duration) (expires time.Duration, version string, finish func(), err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked()
	if ttl <= 0 || ttl > p.options.MaxTTL {
		return 0, "", nil, readError("schema_invalid")
	}
	if p.closed {
		return 0, "", nil, readError("resync_required")
	}
	if len(p.snapshots)+p.pending >= p.options.MaxSnapshots {
		return 0, "", nil, readError("resource_busy")
	}
	p.pending++
	var once sync.Once
	finish = func() {
		once.Do(func() {
			p.mu.Lock()
			p.pending--
			p.mu.Unlock()
		})
	}
	return p.options.Clock() + ttl, p.options.Signer.Version(), finish, nil
}

type readMaterialBudget struct {
	items, bytes, maxItems, maxBytes int
}

func (p *snapshotPages) budget() *readMaterialBudget {
	return &readMaterialBudget{maxItems: p.options.MaxItems, maxBytes: p.options.MaxBytes}
}
func (b *readMaterialBudget) add(value any, entry bool) (readItem, error) {
	raw, err := readEncode(value)
	if err != nil {
		return readItem{}, err
	}
	if entry {
		b.items++
	}
	if len(raw) > b.maxBytes-b.bytes || b.items > b.maxItems {
		return readItem{}, readError("too_large")
	}
	b.bytes += len(raw)
	valueCopy, err := graph.FreezeJSON(value)
	return readItem{valueCopy, len(raw)}, err
}
func (p *snapshotPages) fits(value any) error {
	raw, err := readEncode(value)
	if err != nil {
		return err
	}
	if len(raw) > p.options.ResponseBytes {
		return readError("too_large")
	}
	return nil
}
func (p *snapshotPages) cursor(s *readSnapshot, position readPosition) (string, error) {
	if token := s.cursors[position]; token != "" {
		return token, nil
	}
	if len(p.tokens) >= p.options.MaxCursors {
		return "", readError("resource_busy")
	}
	token, err := p.options.Signer.issue("")
	if err != nil {
		return "", err
	}
	s.cursors[position] = token
	p.tokens[token] = readToken{s, position}
	return token, nil
}

const readCursorPlaceholder = "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx.xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

func (p *snapshotPages) envelope(s *readSnapshot) (map[string]any, error) {
	out, err := readObject(s.envelope)
	if err != nil {
		return nil, err
	}
	out["snapshot"] = s.id
	return out, nil
}
func (p *snapshotPages) stream(s *readSnapshot, name string) ([]readItem, error) {
	for _, stream := range s.streams {
		if stream.name == name {
			return stream.items, nil
		}
	}
	return nil, readError("resync_required")
}
func pageReadValue(items []readItem, complete bool, token string) map[string]any {
	values := make([]any, len(items))
	for i, item := range items {
		values[i] = item.value.JSON()
	}
	out := map[string]any{"items": values, "complete": complete}
	if !complete {
		out["next_cursor"] = token
	}
	return out
}
func (p *snapshotPages) render(s *readSnapshot, positions []readPosition) (FrozenJSON, error) {
	out, err := p.envelope(s)
	if err != nil {
		return FrozenJSON{}, err
	}
	streams := make([][]readItem, len(positions))
	ends := make([]int, len(positions))
	for i, position := range positions {
		items, err := p.stream(s, position.stream)
		if err != nil || position.offset < 0 || position.offset > len(items) {
			return FrozenJSON{}, readError("resync_required")
		}
		streams[i], ends[i] = items, position.offset
		if ends[i] < len(items) {
			ends[i]++
		}
	}
	populate := func(placeholder bool) error {
		for i, pos := range positions {
			complete := ends[i] == len(streams[i])
			token := readCursorPlaceholder
			if !placeholder && !complete {
				var err error
				token, err = p.cursor(s, readPosition{pos.stream, ends[i]})
				if err != nil {
					return err
				}
			}
			value := pageReadValue(streams[i][pos.offset:ends[i]], complete, token)
			if s.relationPages {
				out[pos.stream+"_page"] = value
			} else {
				out["stream"] = pos.stream
				for key, v := range value {
					out[key] = v
				}
				if complete {
					delete(out, "next_cursor")
				}
			}
		}
		return nil
	}
	if err := populate(true); err != nil {
		return FrozenJSON{}, err
	}
	if err := p.fits(out); err != nil {
		return FrozenJSON{}, err
	}
	for i, pos := range positions {
		for ends[i] < len(streams[i]) && ends[i]-pos.offset < s.profile.Limit {
			ends[i]++
			if err := populate(true); err != nil {
				return FrozenJSON{}, err
			}
			if err := p.fits(out); err != nil {
				ends[i]--
				break
			}
		}
	}
	if err := populate(false); err != nil {
		return FrozenJSON{}, err
	}
	if err := p.fits(out); err != nil {
		return FrozenJSON{}, err
	}
	return graph.FreezeJSON(out)
}
func (p *snapshotPages) create(ctx context.Context, s *readSnapshot) (FrozenJSON, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return FrozenJSON{}, err
	}
	if err := p.alive(s); err != nil {
		return FrozenJSON{}, err
	}
	raw, err := s.envelope.Canonical()
	if err != nil {
		return FrozenJSON{}, err
	}
	size, count := len(raw), 0
	for _, stream := range s.streams {
		for _, item := range stream.items {
			if item.size > p.options.MaxBytes-size {
				return FrozenJSON{}, readError("too_large")
			}
			size += item.size
			count++
			if count > p.options.MaxItems {
				return FrozenJSON{}, readError("too_large")
			}
			shell, err := p.envelope(s)
			if err != nil {
				return FrozenJSON{}, err
			}
			page := pageReadValue([]readItem{item}, false, readCursorPlaceholder)
			if s.relationPages {
				shell[stream.name+"_page"] = page
			} else {
				shell["stream"] = stream.name
				for k, v := range page {
					shell[k] = v
				}
			}
			if err := p.fits(shell); err != nil {
				return FrozenJSON{}, err
			}
		}
	}
	if size > p.options.MaxBytes {
		return FrozenJSON{}, readError("too_large")
	}
	if size > p.options.MaxBytes-p.bytes {
		return FrozenJSON{}, readError("resource_busy")
	}
	s.size, s.cursors = size, map[readPosition]string{}
	p.snapshots[s.id], p.bytes = s, p.bytes+size
	var result FrozenJSON
	if s.relationPages {
		positions := make([]readPosition, len(s.streams))
		for i, stream := range s.streams {
			positions[i] = readPosition{stream.name, 0}
		}
		result, err = p.render(s, positions)
	} else {
		out, e := p.envelope(s)
		if e != nil {
			err = e
		} else {
			starts := map[string]any{}
			for _, stream := range s.streams {
				token, e := p.cursor(s, readPosition{stream.name, 0})
				if e != nil {
					err = e
					break
				}
				starts[stream.name] = token
			}
			out["streams"] = starts
			if err == nil {
				err = p.fits(out)
			}
			if err == nil {
				result, err = graph.FreezeJSON(out)
			}
		}
	}
	if err == nil {
		err = p.alive(s)
	}
	if err != nil {
		p.drop(s)
		return FrozenJSON{}, err
	}
	if p.automatic {
		s.timer = time.AfterFunc(s.expires-p.options.Clock(), func() { p.sweep() })
	}
	return result, nil
}
func (p *snapshotPages) page(ctx context.Context, token, stream, binding string) (result FrozenJSON, err error) {
	p.mu.Lock()
	p.sweepLocked()
	entry, ok := p.tokens[token]
	if !ok || !p.options.Signer.valid(token) {
		p.mu.Unlock()
		return FrozenJSON{}, readError("resync_required")
	}
	s := entry.snapshot
	p.mu.Unlock()
	defer func() {
		if err != nil {
			p.discard(s.id)
			err = readFailure(err)
			result = FrozenJSON{}
		}
	}()
	if stream != entry.position.stream || binding != s.binding {
		return FrozenJSON{}, readError("resync_required")
	}
	return s.authorize(ctx, func() (FrozenJSON, error) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return FrozenJSON{}, err
		}
		if p.snapshots[s.id] != s {
			return FrozenJSON{}, readError("resync_required")
		}
		if err := p.alive(s); err != nil {
			return FrozenJSON{}, err
		}
		result, err := p.render(s, []readPosition{entry.position})
		if err == nil {
			err = p.alive(s)
		}
		return result, err
	})
}
