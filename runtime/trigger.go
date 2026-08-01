package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// AppendTrigger merges one event into the response's HX-Trigger header,
// serialized for htmx 2.x: a comma-separated name list while every event
// is payload-less, upgraded to a JSON object ({"name": payload, ...}) as
// soon as any payload is involved. Multiple emissions in one response
// accumulate into a single header; re-emitting an event name replaces its
// payload in place. Event firing order in htmx follows the JSON key
// order, so insertion order is preserved.
//
// Call AppendTrigger before writing the response status or body: headers
// set afterwards are silently discarded by net/http. It reads and rewrites
// the header map, so it is not safe for concurrent use on the same
// response. Serialized values are escaped to ASCII (\uXXXX) because HTTP
// header values cannot carry non-ASCII bytes reliably.
//
// AppendTrigger is plumbing for the generated per-event emitters
// (FR-037): calling it directly with an ad-hoc name forfeits the declared
// event contract — the compiler can neither check the name nor type the
// payload. Do not use it in application code.
func AppendTrigger(w http.ResponseWriter, name string, payload any) error {
	return appendTriggerHeader(w, "HX-Trigger", name, payload)
}

// AppendTriggerAfterSettle merges one event into HX-Trigger-After-Settle:
// htmx fires it after the settle step. Same contract as AppendTrigger.
func AppendTriggerAfterSettle(w http.ResponseWriter, name string, payload any) error {
	return appendTriggerHeader(w, "HX-Trigger-After-Settle", name, payload)
}

// AppendTriggerAfterSwap merges one event into HX-Trigger-After-Swap:
// htmx fires it after the swap step. Same contract as AppendTrigger.
func AppendTriggerAfterSwap(w http.ResponseWriter, name string, payload any) error {
	return appendTriggerHeader(w, "HX-Trigger-After-Swap", name, payload)
}

func appendTriggerHeader(w http.ResponseWriter, headerName, name string, payload any) error {
	if name == "" {
		return fmt.Errorf("ghtmx: event name must not be empty")
	}
	header := w.Header()

	// Collapse any multi-value header into one merged list first.
	events, sawPayload, err := parseTriggerValues(header.Values(headerName))
	if err != nil {
		return err
	}

	var raw json.RawMessage
	if payload != nil {
		raw, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("ghtmx: event %q payload does not serialize: %w", name, err)
		}
		sawPayload = true
	}
	replaced := false
	for i := range events {
		if events[i].name == name {
			events[i].payload = raw
			replaced = true
			break
		}
	}
	if !replaced {
		events = append(events, triggerEvent{name: name, payload: raw})
	}

	// The comma form cannot carry names that look like list or object
	// syntax; those force the JSON form.
	needJSON := sawPayload
	for _, e := range events {
		if strings.ContainsAny(e.name, ",{}\"") {
			needJSON = true
			break
		}
	}

	if !needJSON {
		names := make([]string, len(events))
		for i, e := range events {
			names[i] = e.name
		}
		header.Set(headerName, strings.Join(names, ", "))
		return nil
	}

	var sb bytes.Buffer
	sb.WriteByte('{')
	for i, e := range events {
		if i > 0 {
			sb.WriteByte(',')
		}
		key, err := json.Marshal(e.name)
		if err != nil {
			return err
		}
		sb.Write(key)
		sb.WriteByte(':')
		if e.payload == nil {
			sb.WriteString("null")
			continue
		}
		sb.Write(e.payload)
	}
	sb.WriteByte('}')
	header.Set(headerName, asciiJSON(sb.String()))
	return nil
}

// asciiJSON escapes every non-ASCII rune of serialized JSON as \uXXXX
// (surrogate pairs above the BMP): XHR and fetch decode header values as
// Latin-1, so raw UTF-8 in a header reaches htmx as mojibake.
func asciiJSON(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r < 0x80:
			b.WriteRune(r)
		case r > 0xFFFF:
			r -= 0x10000
			fmt.Fprintf(&b, `\u%04X\u%04X`, 0xD800+(r>>10), 0xDC00+(r&0x3FF))
		default:
			fmt.Fprintf(&b, `\u%04X`, r)
		}
	}
	return b.String()
}

type triggerEvent struct {
	name    string
	payload json.RawMessage // nil for payload-less events
}

// parseTriggerValues reads existing HX-Trigger header values in either
// serialization form into an ordered event list.
func parseTriggerValues(values []string) (events []triggerEvent, sawPayload bool, err error) {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if !strings.HasPrefix(v, "{") {
			for _, name := range strings.Split(v, ",") {
				if name = strings.TrimSpace(name); name != "" {
					events = append(events, triggerEvent{name: name})
				}
			}
			continue
		}
		// JSON object form: preserve key order via the token stream.
		sawPayload = true
		dec := json.NewDecoder(strings.NewReader(v))
		if _, err := dec.Token(); err != nil { // consume '{'
			return nil, false, fmt.Errorf("ghtmx: existing HX-Trigger header is not valid JSON: %w", err)
		}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return nil, false, fmt.Errorf("ghtmx: existing HX-Trigger header is not valid JSON: %w", err)
			}
			key, ok := keyTok.(string)
			if !ok {
				return nil, false, fmt.Errorf("ghtmx: existing HX-Trigger header has a non-string key")
			}
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return nil, false, fmt.Errorf("ghtmx: existing HX-Trigger header is not valid JSON: %w", err)
			}
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				raw = nil
			}
			events = append(events, triggerEvent{name: key, payload: raw})
		}
		// The value must be exactly one object: silently dropping trailing
		// data would lose events on the rewrite.
		if _, err := dec.Token(); err != nil { // consume '}'
			return nil, false, fmt.Errorf("ghtmx: existing HX-Trigger header is not valid JSON: %w", err)
		}
		if dec.InputOffset() < int64(len(v)) && strings.TrimSpace(v[dec.InputOffset():]) != "" {
			return nil, false, fmt.Errorf("ghtmx: existing HX-Trigger header has trailing data after the JSON object")
		}
	}
	return events, sawPayload, nil
}
