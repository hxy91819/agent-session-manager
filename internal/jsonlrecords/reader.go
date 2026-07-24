package jsonlrecords

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// Read visits non-empty JSONL records while bounding retained memory per
// record. Oversized records are drained before parsing resumes so one tool
// result cannot hide the rest of an otherwise valid session.
func Read(r io.Reader, maxRecordBytes int, visit func([]byte) bool) (int, error) {
	reader := bufio.NewReader(r)
	oversizedRecords := 0
	for {
		record, oversized, err := readBoundedRecord(reader, maxRecordBytes)
		if errors.Is(err, io.EOF) {
			return oversizedRecords, nil
		}
		if err != nil {
			return oversizedRecords, err
		}
		if oversized {
			oversizedRecords++
			continue
		}
		record = bytes.TrimSpace(record)
		if len(record) != 0 && !visit(record) {
			return oversizedRecords, nil
		}
	}
}

func readBoundedRecord(reader *bufio.Reader, maxRecordBytes int) ([]byte, bool, error) {
	var record []byte
	oversized := false
	for {
		fragment, isPrefix, err := reader.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) && (len(record) > 0 || oversized) {
				return record, oversized, nil
			}
			return nil, false, err
		}
		if !oversized {
			if len(fragment) > maxRecordBytes-len(record) {
				record = nil
				oversized = true
			} else {
				record = append(record, fragment...)
			}
		}
		if !isPrefix {
			return record, oversized, nil
		}
	}
}
