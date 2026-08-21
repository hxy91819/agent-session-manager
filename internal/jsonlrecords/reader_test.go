package jsonlrecords

import (
	"strings"
	"testing"
)

func TestReadSkipsOversizedRecordAndContinues(t *testing.T) {
	var records []string
	oversized, err := Read(strings.NewReader("12345\n123456\nlast"), 5, func(record []byte) bool {
		records = append(records, string(record))
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if oversized != 1 {
		t.Fatalf("oversized records = %d, want 1", oversized)
	}
	want := []string{"12345", "last"}
	if strings.Join(records, "|") != strings.Join(want, "|") {
		t.Fatalf("records = %#v, want %#v", records, want)
	}
}

func TestReadKeepsBufferSizedFinalRecord(t *testing.T) {
	want := strings.Repeat("x", 4096)
	var records []string
	oversized, err := Read(strings.NewReader(want), 8192, func(record []byte) bool {
		records = append(records, string(record))
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if oversized != 0 {
		t.Fatalf("oversized records = %d, want 0", oversized)
	}
	if len(records) != 1 || records[0] != want {
		t.Fatalf("records = %d items, want one %d-byte record", len(records), len(want))
	}
}

func TestReadCountsBufferSizedOversizedFinalRecord(t *testing.T) {
	oversized, err := Read(strings.NewReader(strings.Repeat("x", 8192)), 4096, func(record []byte) bool {
		t.Fatalf("visited oversized record with %d bytes", len(record))
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if oversized != 1 {
		t.Fatalf("oversized records = %d, want 1", oversized)
	}
}

func TestReadWithOversizedKeepsBoundedPrefix(t *testing.T) {
	var got OversizedRecord
	oversized, err := ReadWithOversized(
		strings.NewReader("0123456789\nok\n"),
		5,
		4,
		func([]byte) bool { return true },
		func(record OversizedRecord) { got = record },
	)
	if err != nil {
		t.Fatal(err)
	}
	if oversized != 1 {
		t.Fatalf("oversized = %d, want 1", oversized)
	}
	if string(got.Prefix) != "0123" || got.Bytes != 10 {
		t.Fatalf("record = %#v, want prefix 0123 and 10 bytes", got)
	}
	if string(got.Suffix) != "6789" {
		t.Fatalf("record suffix = %q, want 6789", got.Suffix)
	}
}

func TestTruncatedStringFieldKeepsBothEdges(t *testing.T) {
	record := OversizedRecord{
		Prefix: []byte(`{"role":"user","content":"HEAD-` + strings.Repeat("x", 20)),
		Suffix: []byte(strings.Repeat("x", 20) + `-TAIL"}`),
	}
	got, ok := TruncatedStringField(record, "content", 12)
	if !ok || !strings.Contains(got, "HEAD-") || !strings.Contains(got, "-TAIL") {
		t.Fatalf("recovered = %q ok=%v, want both edges", got, ok)
	}
}

func TestReadWithOffsetsReportsExactRecordStarts(t *testing.T) {
	body := "{\"a\":1}\n{\"b\":\"乙丙丁\"}\n" + "{\"big\":\"" +
		strings.Repeat("x", 64) + "\"}\n" + "{\"tail\":true}"
	type visit struct {
		offset int64
		line   string
	}
	var visits []visit
	var oversizedOffsets []int64
	_, err := ReadWithOffsets(
		strings.NewReader(body),
		32, // force only the {big:...} record into the oversized path
		0,
		func(record []byte, offset int64) bool {
			visits = append(visits, visit{offset, string(record)})
			return true
		},
		func(record OversizedRecord, offset int64) {
			oversizedOffsets = append(oversizedOffsets, offset)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(visits) != 3 || len(oversizedOffsets) != 1 {
		t.Fatalf("visits=%v oversized=%v", visits, oversizedOffsets)
	}
	if visits[0].offset != 0 || visits[1].offset != 8 {
		t.Fatalf("plain offsets: %#v", visits)
	}
	bigStart := int64(strings.Index(body, "{\"big\":"))
	if oversizedOffsets[0] != bigStart {
		t.Fatalf("oversized offset = %d, want %d", oversizedOffsets[0], bigStart)
	}
	tailStart := int64(strings.Index(body, "{\"tail\":"))
	if visits[2].offset != tailStart {
		t.Fatalf("tail offset = %d, want %d", visits[2].offset, tailStart)
	}
}
