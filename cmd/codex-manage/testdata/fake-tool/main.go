package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type request struct {
	ID     int    `json:"id"`
	Method string `json:"method"`
}

type observation struct {
	Args                []string `json:"args"`
	CodexHome           string   `json:"codexHome"`
	SQLiteHome          string   `json:"sqliteHome"`
	Config              string   `json:"config"`
	HTTPSProxy          string   `json:"httpsProxy"`
	HasCodexAccessToken bool     `json:"hasCodexAccessToken"`
	HasCodexAPIKey      bool     `json:"hasCodexApiKey"`
	HasOpenAIAPIKey     bool     `json:"hasOpenaiApiKey"`
	ReceivedInitialize  bool     `json:"receivedInitialize"`
	ReceivedInitialized bool     `json:"receivedInitialized"`
	ReceivedLoginStart  bool     `json:"receivedLoginStart"`
	ReceivedLoginCancel bool     `json:"receivedLoginCancel"`
}

func main() {
	name := strings.ToLower(filepath.Base(os.Args[0]))
	if strings.Contains(name, "browser") {
		runBrowser()
		return
	}
	if err := runCodex(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runBrowser() {
	data, err := json.Marshal(os.Args[1:])
	if err != nil {
		os.Exit(1)
	}
	if err := os.WriteFile(os.Getenv("FAKE_BROWSER_LOG"), append(data, '\n'), 0o600); err != nil {
		os.Exit(1)
	}
}

func runCodex() error {
	responses, err := readLines(os.Getenv("FAKE_CODEX_SCENARIO_FILE"))
	if err != nil {
		return err
	}
	if len(responses) < 2 {
		return fmt.Errorf("scenario requires initialize and login responses")
	}

	config, err := os.ReadFile(filepath.Join(os.Getenv("CODEX_HOME"), "config.toml"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	observed := observation{
		Args:                os.Args[1:],
		CodexHome:           os.Getenv("CODEX_HOME"),
		SQLiteHome:          os.Getenv("CODEX_SQLITE_HOME"),
		Config:              string(config),
		HTTPSProxy:          os.Getenv("HTTPS_PROXY"),
		HasCodexAccessToken: os.Getenv("CODEX_ACCESS_TOKEN") != "",
		HasCodexAPIKey:      os.Getenv("CODEX_API_KEY") != "",
		HasOpenAIAPIKey:     os.Getenv("OPENAI_API_KEY") != "",
	}

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var message request
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			return err
		}
		switch message.Method {
		case "initialize":
			observed.ReceivedInitialize = true
			fmt.Println(responses[0])
		case "initialized":
			observed.ReceivedInitialized = true
		case "account/login/start":
			observed.ReceivedLoginStart = true
			fmt.Println(responses[1])
			if source := os.Getenv("FAKE_CODEX_AUTH_FILE"); source != "" {
				if err := copyFile(source, filepath.Join(observed.CodexHome, "auth.json")); err != nil {
					return err
				}
			}
			if err := writeObservation(observed); err != nil {
				return err
			}
			for _, response := range responses[2:] {
				fmt.Println(response)
			}
		case "account/login/cancel":
			observed.ReceivedLoginCancel = true
			if err := writeObservation(observed); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

func readLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, scanner.Err()
}

func copyFile(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, data, 0o600)
}

func writeObservation(observed observation) error {
	data, err := json.Marshal(observed)
	if err != nil {
		return err
	}
	return os.WriteFile(os.Getenv("FAKE_CODEX_LOG"), append(data, '\n'), 0o600)
}
