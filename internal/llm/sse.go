package llm

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

type sseEvent struct {
	Event string
	Data  string
}

func readSSE(reader io.Reader, emit func(sseEvent) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
	var eventName string
	var dataLines []string
	dispatch := func() error {
		if len(dataLines) == 0 {
			eventName = ""
			return nil
		}
		event := sseEvent{Event: eventName, Data: strings.Join(dataLines, "\n")}
		eventName = ""
		dataLines = nil
		return emit(event)
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			eventName = value
		case "data":
			dataLines = append(dataLines, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return dispatch()
}

func jsonString(value any, fields ...string) string {
	current := value
	for _, field := range fields {
		m, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = m[field]
	}
	text, _ := current.(string)
	return text
}

func decodeMap(data string) (map[string]any, error) {
	var payload map[string]any
	err := json.Unmarshal([]byte(data), &payload)
	return payload, err
}
