package jsonlrecords

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

type OversizedRecord struct {
	Prefix []byte
	Suffix []byte
	Bytes  int
}

// Read visits non-empty JSONL records while bounding retained memory per
// record. Oversized records are drained before parsing resumes so one tool
// result cannot hide the rest of an otherwise valid session.
func Read(r io.Reader, maxRecordBytes int, visit func([]byte) bool) (int, error) {
	return ReadWithOversized(r, maxRecordBytes, 0, visit, nil)
}

// ReadWithOversized exposes bounded record edges for oversized records.
// Providers can distinguish huge tool payloads from user-authored messages and,
// when their format permits it, recover a head/tail preview without retaining
// the full record.
func ReadWithOversized(
	r io.Reader,
	maxRecordBytes int,
	prefixBytes int,
	visit func([]byte) bool,
	visitOversized func(OversizedRecord),
) (int, error) {
	reader := bufio.NewReader(r)
	oversizedRecords := 0
	for {
		record, oversizedRecord, oversized, err := readBoundedRecordWithEdges(
			reader,
			maxRecordBytes,
			prefixBytes,
		)
		if errors.Is(err, io.EOF) {
			return oversizedRecords, nil
		}
		if err != nil {
			return oversizedRecords, err
		}
		if oversized {
			oversizedRecords++
			if visitOversized != nil {
				visitOversized(oversizedRecord)
			}
			continue
		}
		record = bytes.TrimSpace(record)
		if len(record) != 0 && !visit(record) {
			return oversizedRecords, nil
		}
	}
}

func readBoundedRecordWithEdges(
	reader *bufio.Reader,
	maxRecordBytes int,
	edgeBytes int,
) ([]byte, OversizedRecord, bool, error) {
	var record []byte
	var prefix []byte
	var suffix []byte
	totalBytes := 0
	oversized := false
	for {
		fragment, isPrefix, err := reader.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) && (len(record) > 0 || oversized) {
				return record, OversizedRecord{
					Prefix: prefix,
					Suffix: suffix,
					Bytes:  totalBytes,
				}, oversized, nil
			}
			return nil, OversizedRecord{}, false, err
		}
		totalBytes += len(fragment)
		if len(prefix) < edgeBytes {
			remaining := edgeBytes - len(prefix)
			if remaining > len(fragment) {
				remaining = len(fragment)
			}
			prefix = append(prefix, fragment[:remaining]...)
		}
		if !oversized {
			if len(fragment) > maxRecordBytes-len(record) {
				suffix = appendRecordSuffix(suffix, record, edgeBytes)
				suffix = appendRecordSuffix(suffix, fragment, edgeBytes)
				record = nil
				oversized = true
			} else {
				record = append(record, fragment...)
			}
		} else {
			suffix = appendRecordSuffix(suffix, fragment, edgeBytes)
		}
		if !isPrefix {
			return record, OversizedRecord{
				Prefix: prefix,
				Suffix: suffix,
				Bytes:  totalBytes,
			}, oversized, nil
		}
	}
}

func appendRecordSuffix(suffix []byte, fragment []byte, limit int) []byte {
	if limit <= 0 || len(fragment) == 0 {
		return suffix
	}
	if len(fragment) >= limit {
		return append(suffix[:0], fragment[len(fragment)-limit:]...)
	}
	overflow := len(suffix) + len(fragment) - limit
	if overflow > 0 {
		copy(suffix, suffix[overflow:])
		suffix = suffix[:len(suffix)-overflow]
	}
	return append(suffix, fragment...)
}

// TruncatedStringField recovers one JSON string field from bounded record
// edges. It is intentionally narrow: providers must first prove the record is
// a user-authored shape before treating the recovered text as report evidence.
func TruncatedStringField(
	record OversizedRecord,
	field string,
	maxEdgeBytes int,
) (string, bool) {
	key := []byte(`"` + field + `"`)
	keyIndex := bytes.Index(record.Prefix, key)
	if keyIndex < 0 {
		return "", false
	}
	valueStart, ok := jsonStringValueStart(record.Prefix[keyIndex+len(key):])
	if !ok {
		return "", false
	}
	valueStart += keyIndex + len(key)
	rawHead := record.Prefix[valueStart:]
	if valueEnd, complete := jsonStringRawEnd(rawHead); complete {
		var value string
		if json.Unmarshal(appendQuoted(nil, rawHead[:valueEnd]), &value) == nil {
			return value, true
		}
		return "", false
	}

	head, ok := decodeJSONStringHead(rawHead, maxEdgeBytes)
	if !ok {
		return "", false
	}
	tail, tailOK := decodeJSONStringTail(record.Suffix, maxEdgeBytes)
	if !tailOK {
		return head + " … [oversized message remainder omitted]", true
	}
	return head + " … [oversized message middle omitted] … " + tail, true
}

// CompleteStringField extracts a complete, bounded JSON string field from
// data such as an oversized record prefix.
func CompleteStringField(data []byte, field string) (string, bool) {
	key := []byte(`"` + field + `"`)
	keyIndex := bytes.Index(data, key)
	if keyIndex < 0 {
		return "", false
	}
	valueStart, ok := jsonStringValueStart(data[keyIndex+len(key):])
	if !ok {
		return "", false
	}
	valueStart += keyIndex + len(key)
	valueEnd, ok := jsonStringRawEnd(data[valueStart:])
	if !ok {
		return "", false
	}
	var value string
	if json.Unmarshal(
		appendQuoted(nil, data[valueStart:valueStart+valueEnd]),
		&value,
	) != nil {
		return "", false
	}
	return value, true
}

// CompleteScalarField returns one complete JSON scalar value, including its
// quotes when it is a string. It is used for bounded metadata such as provider
// timestamps that may be encoded as either strings or numbers.
func CompleteScalarField(data []byte, field string) ([]byte, bool) {
	key := []byte(`"` + field + `"`)
	keyIndex := bytes.Index(data, key)
	if keyIndex < 0 {
		return nil, false
	}
	rest := data[keyIndex+len(key):]
	colon := bytes.IndexByte(rest, ':')
	if colon < 0 {
		return nil, false
	}
	start := colon + 1
	for start < len(rest) {
		switch rest[start] {
		case ' ', '\t', '\r', '\n':
			start++
		default:
			goto value
		}
	}
	return nil, false

value:
	if rest[start] == '"' {
		end, ok := jsonStringRawEnd(rest[start+1:])
		if !ok {
			return nil, false
		}
		return append([]byte(nil), rest[start:start+end+2]...), true
	}
	end := start
	for end < len(rest) {
		switch rest[end] {
		case ',', '}', ']', ' ', '\t', '\r', '\n':
			if end == start {
				return nil, false
			}
			return append([]byte(nil), rest[start:end]...), true
		default:
			end++
		}
	}
	return nil, false
}

// Compact removes JSON whitespace for bounded structural checks. Callers
// should match producer-owned fields near the start of a record, not arbitrary
// user text, before using the result for classification.
func Compact(data []byte) []byte {
	compact := make([]byte, 0, len(data))
	for _, value := range data {
		switch value {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			compact = append(compact, value)
		}
	}
	return compact
}

func jsonStringValueStart(data []byte) (int, bool) {
	colon := bytes.IndexByte(data, ':')
	if colon < 0 {
		return 0, false
	}
	for index := colon + 1; index < len(data); index++ {
		switch data[index] {
		case ' ', '\t', '\r', '\n':
			continue
		case '"':
			return index + 1, true
		default:
			return 0, false
		}
	}
	return 0, false
}

func jsonStringRawEnd(data []byte) (int, bool) {
	escaped := false
	for index, value := range data {
		if escaped {
			escaped = false
			continue
		}
		switch value {
		case '\\':
			escaped = true
		case '"':
			return index, true
		}
	}
	return 0, false
}

func decodeJSONStringHead(raw []byte, maxBytes int) (string, bool) {
	if len(raw) > maxBytes {
		raw = raw[:maxBytes]
	}
	for trim := 0; trim <= 8 && len(raw)-trim > 0; trim++ {
		var value string
		if json.Unmarshal(appendQuoted(nil, raw[:len(raw)-trim]), &value) == nil {
			return value, true
		}
	}
	return "", false
}

func decodeJSONStringTail(suffix []byte, maxBytes int) (string, bool) {
	closing := lastUnescapedQuote(suffix)
	if closing < 0 || !onlyJSONClosers(suffix[closing+1:]) {
		return "", false
	}
	raw := suffix[:closing]
	if len(raw) > maxBytes {
		raw = raw[len(raw)-maxBytes:]
	}
	for skip := 0; skip <= 8 && skip < len(raw); skip++ {
		var value string
		if json.Unmarshal(appendQuoted(nil, raw[skip:]), &value) == nil {
			return value, true
		}
	}
	return "", false
}

func lastUnescapedQuote(data []byte) int {
	for index := len(data) - 1; index >= 0; index-- {
		if data[index] != '"' {
			continue
		}
		backslashes := 0
		for previous := index - 1; previous >= 0 && data[previous] == '\\'; previous-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			return index
		}
	}
	return -1
}

func onlyJSONClosers(data []byte) bool {
	for _, value := range data {
		switch value {
		case ' ', '\t', '\r', '\n', '}', ']', ',':
			continue
		default:
			return false
		}
	}
	return true
}

func appendQuoted(dst []byte, raw []byte) []byte {
	dst = append(dst, '"')
	dst = append(dst, raw...)
	return append(dst, '"')
}
