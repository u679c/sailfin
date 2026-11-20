package main

import (
	"fmt"
	"strings"
	"unicode"
)

type nginxNode struct {
	args       []string
	children   []nginxNode
	start, end int
	close      int // zero for semicolon directives
}

type nginxToken struct {
	value      string
	start, end int
	symbol     bool
}

// Parse structure rather than looking for a brace or server_name with a regex.
func parseNginx(text string) ([]nginxNode, error) {
	var tokens []nginxToken
	for i := 0; i < len(text); {
		switch {
		case unicode.IsSpace(rune(text[i])):
			i++
		case text[i] == '#':
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case strings.ContainsRune("{};", rune(text[i])):
			tokens = append(tokens, nginxToken{string(text[i]), i, i + 1, true})
			i++
		default:
			start := i
			var word strings.Builder
			var quote byte
			for i < len(text) {
				c := text[i]
				if c == '\\' && i+1 < len(text) {
					word.WriteByte(text[i+1])
					i += 2
					continue
				}
				if quote != 0 {
					if c == quote {
						quote = 0
					} else {
						word.WriteByte(c)
					}
				} else {
					switch {
					case c == '"' || c == '\'':
						quote = c
					case c == '$' && i+1 < len(text) && text[i+1] == '{':
						end := strings.IndexByte(text[i+2:], '}')
						if end < 0 {
							return nil, fmt.Errorf("unterminated Nginx variable")
						}
						end += i + 3
						word.WriteString(text[i:end])
						i = end
						continue
					case unicode.IsSpace(rune(c)) || strings.ContainsRune("{};#", rune(c)):
						goto wordEnd
					default:
						word.WriteByte(c)
					}
				}
				i++
			}
		wordEnd:
			if quote != 0 {
				return nil, fmt.Errorf("unterminated Nginx quote")
			}
			tokens = append(tokens, nginxToken{word.String(), start, i, false})
		}
	}

	i := 0
	var block func(int) ([]nginxNode, int, error)
	block = func(depth int) ([]nginxNode, int, error) {
		if depth > 50 {
			return nil, 0, fmt.Errorf("Nginx config is nested too deeply")
		}
		var nodes []nginxNode
		var args []string
		start := 0
		for i < len(tokens) {
			token := tokens[i]
			i++
			if !token.symbol {
				if len(args) == 0 {
					start = token.start
				}
				args = append(args, token.value)
				continue
			}
			if token.value == "}" && depth > 0 && len(args) == 0 {
				return nodes, token.start, nil
			}
			if len(args) == 0 || token.value == "}" {
				return nil, 0, fmt.Errorf("unexpected Nginx delimiter %q", token.value)
			}
			node := nginxNode{args: args, start: start, end: token.end}
			args = nil
			if token.value == "{" {
				var err error
				node.children, node.close, err = block(depth + 1)
				if err != nil {
					return nil, 0, err
				}
				node.end = node.close + 1
			}
			nodes = append(nodes, node)
		}
		if len(args) != 0 || depth > 0 {
			return nil, 0, fmt.Errorf("incomplete Nginx directive or block")
		}
		return nodes, 0, nil
	}
	nodes, _, err := block(0)
	return nodes, err
}

func nginxDirective(nodes []nginxNode, name string) []nginxNode {
	var matches []nginxNode
	for _, node := range nodes {
		if node.args[0] == name {
			matches = append(matches, node)
		}
	}
	return matches
}

func nginxHasArg(nodes []nginxNode, directive, value string) bool {
	for _, node := range nginxDirective(nodes, directive) {
		for _, arg := range node.args[1:] {
			if arg == value {
				return true
			}
		}
	}
	return false
}

func editNginxSite(text, serverName, listen, location, id, relay string) (string, bool, error) {
	nodes, err := parseNginx(text)
	if err != nil {
		return "", false, err
	}
	servers := nginxDirective(nodes, "server")
	for _, http := range nginxDirective(nodes, "http") {
		servers = append(servers, nginxDirective(http.children, "server")...)
	}
	var matches []nginxNode
	for _, server := range servers {
		if nginxHasArg(server.children, "server_name", serverName) &&
			(listen == "" || nginxHasArg(server.children, "listen", listen)) {
			matches = append(matches, server)
		}
	}
	if len(matches) != 1 {
		return "", false, fmt.Errorf("expected one server block for %s, found %d; check --site-file or add --listen", serverName, len(matches))
	}
	server := matches[0]
	for _, names := range nginxDirective(server.children, "server_name") {
		for _, alias := range names.args[1:] {
			if alias != serverName {
				return "", false, fmt.Errorf("server block also serves %s; use an unshared server block to limit injection to %s", alias, serverName)
			}
		}
	}
	var html []nginxNode
	relayFound := false
	for _, loc := range nginxDirective(server.children, "location") {
		if strings.Join(loc.args[1:], " ") == location {
			html = append(html, loc)
		}
		for _, arg := range loc.args[1:] {
			if arg == "/__tracelite" || strings.HasPrefix(arg, "/__tracelite/") {
				relayFound = true
			}
		}
	}
	if len(html) != 1 {
		return "", false, fmt.Errorf("expected one location %s in server %s, found %d", location, serverName, len(html))
	}
	page := html[0]
	if relayFound || nginxHasArg(page.children, "sub_filter", traceLiteHTML) {
		relayBlock := nginxRelayBlock(id, relay)
		if strings.Contains(text[server.start:server.end], relayBlock) &&
			nginxHasArg(page.children, "sub_filter", traceLiteHTML) {
			return text, false, nil
		}
		return "", false, fmt.Errorf("existing TraceLite route or injection conflicts with this configuration")
	}
	if len(nginxDirective(page.children, "include")) != 0 {
		return "", false, fmt.Errorf("target location contains include directives; use an explicit location to apply this command")
	}
	if len(nginxDirective(page.children, "sub_filter_types")) != 0 ||
		len(nginxDirective(server.children, "sub_filter_types")) != 0 ||
		len(nginxDirective(server.children, "sub_filter")) != 0 ||
		len(nginxDirective(page.children, "sub_filter")) != 0 {
		return "", false, fmt.Errorf("target server or location has custom sub_filter settings; review before adding injection")
	}

	pageBlock := "\n        # TraceLite: inject the browser SDK into HTML.\n" +
		"        sub_filter '</head>' '" + traceLiteHTML + "';\n"
	headers := nginxDirective(page.children, "proxy_set_header")
	// Adding a header to a location that inherits headers would drop all inherited
	// headers. Keep that inheritance unless the headers are declared in this scope.
	if len(headers) > 0 && len(nginxDirective(page.children, "proxy_pass")) > 0 {
		hasEncoding := false
		for _, header := range headers {
			if len(header.args) == 3 && strings.EqualFold(header.args[1], "Accept-Encoding") {
				if header.args[2] != "" {
					return "", false, fmt.Errorf("target location already sets upstream Accept-Encoding")
				}
				hasEncoding = true
			}
		}
		if !hasEncoding {
			pageBlock += "        proxy_set_header Accept-Encoding \"\";\n"
		}
	}
	updated := text[:page.close] + pageBlock + "    " + text[page.close:server.close] +
		nginxRelayBlock(id, relay) + text[server.close:]
	return updated, true, nil
}

const traceLiteHTML = `<script type="module" src="/__tracelite/boot.js"></script></head>`

func nginxRelayBlock(id, relay string) string {
	return fmt.Sprintf(`
    # TraceLite: serve the SDK and relay same-origin events.
    location ^~ /__tracelite/ {
        access_log off;
        client_max_body_size 64k;
        proxy_pass http://%s;
        proxy_set_header X-TraceLite-Site %s;
        proxy_set_header Authorization "";
        proxy_set_header Cookie "";
        proxy_read_timeout 10s;
    }
`, relay, id)
}
