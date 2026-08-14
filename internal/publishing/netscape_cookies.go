package publishing

import (
	"bufio"
	"bytes"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type cookieValues struct {
	Header string
	Values map[string]string
}

func cookiesForURL(raw []byte, target string, now time.Time) (cookieValues, error) {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Hostname() == "" {
		return cookieValues{}, fmt.Errorf("parse cookie target: %w", err)
	}
	host := strings.ToLower(parsed.Hostname())
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	secure := parsed.Scheme == "https"
	values := map[string]string{}
	order := make([]string, 0)
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			if !strings.HasPrefix(line, "#HttpOnly_") {
				continue
			}
			line = strings.TrimPrefix(line, "#HttpOnly_")
		}
		fields := strings.SplitN(line, "\t", 7)
		if len(fields) != 7 {
			continue
		}
		domain := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(fields[0]), "."))
		if domain == "" || (host != domain && !strings.HasSuffix(host, "."+domain)) {
			continue
		}
		cookiePath := strings.TrimSpace(fields[2])
		if cookiePath == "" {
			cookiePath = "/"
		}
		if !strings.HasPrefix(path, cookiePath) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(fields[3]), "TRUE") && !secure {
			continue
		}
		if expires, parseErr := strconv.ParseInt(strings.TrimSpace(fields[4]), 10, 64); parseErr == nil &&
			expires > 0 &&
			now.Unix() >= expires {
			continue
		}
		name := strings.TrimSpace(fields[5])
		value := strings.TrimSpace(fields[6])
		if name == "" || strings.ContainsAny(name+value, "\r\n;") {
			continue
		}
		if _, exists := values[name]; !exists {
			order = append(order, name)
		}
		values[name] = value
	}
	if err := scanner.Err(); err != nil {
		return cookieValues{}, fmt.Errorf("read Netscape cookies: %w", err)
	}
	pairs := make([]string, 0, len(order))
	for _, name := range order {
		pairs = append(pairs, name+"="+values[name])
	}
	return cookieValues{
		Header: strings.Join(pairs, "; "),
		Values: values,
	}, nil
}
