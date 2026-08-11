package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type invocation struct {
	Program string            `json:"program"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

func main() {
	args := os.Args[1:]
	if len(args) >= 2 && args[0] == "agent" && args[1] == "list" {
		recordCall()
		if message := os.Getenv("ASM_FAKE_HERDR_LIST_ERROR"); message != "" {
			fmt.Fprintln(os.Stderr, message)
			os.Exit(1)
		}
		fmt.Println(os.Getenv("ASM_FAKE_HERDR_LIST_JSON"))
		return
	}
	if len(args) == 3 && args[0] == "agent" && args[1] == "focus" {
		recordCall()
		if message := os.Getenv("ASM_FAKE_HERDR_FOCUS_ERROR"); message != "" {
			fmt.Fprintln(os.Stderr, message)
			os.Exit(1)
		}
		if path := os.Getenv("ASM_FAKE_HERDR_FOCUS_FILE"); path != "" {
			mustWrite(path, []byte(args[2]))
		}
		fmt.Printf("{\"result\":{\"agent\":{\"pane_id\":%q}}}\n", args[2])
		return
	}

	record := invocation{
		Program: filepath.Base(os.Args[0]),
		Args:    args,
		Env:     map[string]string{},
	}
	for _, key := range []string{
		"HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_SESSION", "HERDR_WORKSPACE_ID",
		"HERDR_TAB_ID", "HERDR_PANE_ID",
	} {
		record.Env[key] = os.Getenv(key)
	}
	data, err := json.Marshal(record)
	if err != nil {
		panic(err)
	}
	if path := os.Getenv("ASM_FAKE_AGENT_FILE"); path != "" {
		mustWrite(path, data)
	}
	fmt.Println(string(data))
}

func recordCall() {
	path := os.Getenv("ASM_FAKE_HERDR_CALL_FILE")
	if path == "" {
		return
	}
	line := strings.Join(os.Args[1:], " ") + "\n"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	if _, err := file.WriteString(line); err != nil {
		panic(err)
	}
}

func mustWrite(path string, data []byte) {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		panic(err)
	}
}
