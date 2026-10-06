package nvx

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// A reader for the part of YAML that pnpm-lock.yaml and Yarn 2+'s yarn.lock
// are written in, so the module keeps no dependencies.
//
// It reads block mappings and sequences, single-line flow mappings and
// sequences, plain, single-quoted and double-quoted scalars, block scalars,
// comments, and documents separated by "---". Anchors, aliases, tags, complex
// keys, duplicate keys and tab indentation are errors: pnpm and Yarn do not
// write them, and a lockfile this cannot read is reported, never read in part.
//
// Errors name a line number and never the line's text: a lockfile can hold a
// tarball URL with a token in it, and the error reaches the audit log.

type yamlKind int

const (
	yamlNull yamlKind = iota
	yamlScalar
	yamlMap
	yamlSeq
)

type yamlNode struct {
	kind   yamlKind
	value  string
	keys   []string // a mapping's keys, in file order
	fields map[string]*yamlNode
	items  []*yamlNode
	line   int
}

// get returns a mapping's value for key, or nil.
func (n *yamlNode) get(key string) *yamlNode {
	if n == nil || n.kind != yamlMap {
		return nil
	}
	return n.fields[key]
}

// str is a scalar's text, or "" for anything else.
func (n *yamlNode) str() string {
	if n == nil || n.kind != yamlScalar {
		return ""
	}
	return n.value
}

// list reads a sequence of scalars, or a single scalar, as a list.
func (n *yamlNode) list() []string {
	if n == nil {
		return nil
	}
	switch n.kind {
	case yamlScalar:
		return []string{n.value}
	case yamlSeq:
		out := make([]string, 0, len(n.items))
		for _, it := range n.items {
			out = append(out, it.str())
		}
		return out
	}
	return nil
}

// stringMap reads a mapping of scalars, the shape of a dependency list.
func (n *yamlNode) stringMap() map[string]string {
	if n == nil || n.kind != yamlMap {
		return nil
	}
	out := make(map[string]string, len(n.keys))
	for _, k := range n.keys {
		out[k] = n.fields[k].str()
	}
	return out
}

type yamlError struct {
	line int
	msg  string
}

func (e *yamlError) Error() string { return fmt.Sprintf("line %d: %s", e.line, e.msg) }

type yamlLine struct {
	num    int
	indent int
	text   string // without the indentation
}

type yamlParser struct {
	lines []yamlLine
	pos   int
}

// parseYAMLDocuments reads every document in data.
func parseYAMLDocuments(data []byte) ([]*yamlNode, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("it is not UTF-8 text")
	}
	text := strings.TrimPrefix(string(data), string(rune(0xFEFF)))
	var docs []*yamlNode
	var cur []yamlLine
	flush := func() error {
		p := &yamlParser{lines: cur}
		if p.skipBlank(); p.pos >= len(p.lines) {
			cur = nil
			return nil
		}
		n, err := p.parseBlock(0)
		if err != nil {
			return err
		}
		if p.skipBlank(); p.pos < len(p.lines) {
			return &yamlError{p.lines[p.pos].num, "unexpected indentation"}
		}
		docs = append(docs, n)
		cur = nil
		return nil
	}
	for i, raw := range strings.Split(text, "\n") {
		raw = strings.TrimSuffix(raw, "\r")
		if raw == "---" || strings.HasPrefix(raw, "--- ") || raw == "..." {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		if strings.HasPrefix(raw, "%") {
			return nil, &yamlError{i + 1, "a YAML directive"}
		}
		trimmed := strings.TrimLeft(raw, " ")
		if strings.HasPrefix(trimmed, "\t") {
			return nil, &yamlError{i + 1, "tab indentation"}
		}
		cur = append(cur, yamlLine{num: i + 1, indent: len(raw) - len(trimmed), text: strings.TrimRight(trimmed, " \t")})
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return docs, nil
}

func isBlankOrComment(l yamlLine) bool { return l.text == "" || strings.HasPrefix(l.text, "#") }

func (p *yamlParser) skipBlank() {
	for p.pos < len(p.lines) && isBlankOrComment(p.lines[p.pos]) {
		p.pos++
	}
}

// peek returns the next line that is not blank or a comment.
func (p *yamlParser) peek() (yamlLine, bool) {
	p.skipBlank()
	if p.pos >= len(p.lines) {
		return yamlLine{}, false
	}
	return p.lines[p.pos], true
}

func isSeqItem(text string) bool { return text == "-" || strings.HasPrefix(text, "- ") }

// parseBlock reads the block that starts at the next line, which must be
// indented at least minIndent.
func (p *yamlParser) parseBlock(minIndent int) (*yamlNode, error) {
	l, ok := p.peek()
	if !ok || l.indent < minIndent {
		return &yamlNode{kind: yamlNull}, nil
	}
	if isSeqItem(l.text) {
		return p.parseSeq(l.indent)
	}
	return p.parseMap(l.indent)
}

func (p *yamlParser) parseMap(indent int) (*yamlNode, error) {
	n := &yamlNode{kind: yamlMap, fields: map[string]*yamlNode{}}
	for {
		l, ok := p.peek()
		if !ok || l.indent < indent {
			return n, nil
		}
		if n.line == 0 {
			n.line = l.num
		}
		if l.indent > indent {
			return nil, &yamlError{l.num, "unexpected indentation"}
		}
		if isSeqItem(l.text) {
			return nil, &yamlError{l.num, "a sequence item inside a mapping"}
		}
		key, rest, err := splitYAMLKey(l.text)
		if err != nil {
			return nil, &yamlError{l.num, err.Error()}
		}
		if _, dup := n.fields[key]; dup {
			return nil, &yamlError{l.num, "a key that appears twice in one mapping"}
		}
		p.pos++
		var val *yamlNode
		switch {
		case rest == "":
			next, ok := p.peek()
			switch {
			case ok && next.indent > indent:
				val, err = p.parseBlock(indent + 1)
			case ok && next.indent == indent && isSeqItem(next.text):
				val, err = p.parseSeq(indent)
			default:
				val = &yamlNode{kind: yamlNull}
			}
		case rest[0] == '|' || rest[0] == '>':
			val, err = p.blockScalar(rest, indent, l.num)
		default:
			val, err = parseYAMLInline(rest, l.num)
		}
		if err != nil {
			return nil, err
		}
		val.line = l.num
		n.keys = append(n.keys, key)
		n.fields[key] = val
	}
}

func (p *yamlParser) parseSeq(indent int) (*yamlNode, error) {
	n := &yamlNode{kind: yamlSeq}
	for {
		l, ok := p.peek()
		if !ok || l.indent < indent || (l.indent == indent && !isSeqItem(l.text)) {
			return n, nil
		}
		if l.indent > indent {
			return nil, &yamlError{l.num, "unexpected indentation"}
		}
		item := strings.TrimLeft(strings.TrimPrefix(l.text, "-"), " ")
		var val *yamlNode
		var err error
		switch {
		case item == "":
			p.pos++
			val, err = p.parseBlock(indent + 1)
		case startsYAMLMapping(item):
			// "- key: value" opens a mapping whose keys line up with "key".
			p.lines[p.pos] = yamlLine{num: l.num, indent: l.indent + len(l.text) - len(item), text: item}
			val, err = p.parseMap(p.lines[p.pos].indent)
		default:
			p.pos++
			val, err = parseYAMLInline(item, l.num)
		}
		if err != nil {
			return nil, err
		}
		n.items = append(n.items, val)
	}
}

// startsYAMLMapping reports a line that is "key: value" or "key:", as opposed
// to a scalar or a flow collection.
func startsYAMLMapping(text string) bool {
	if text == "" || strings.ContainsRune("[{", rune(text[0])) {
		return false
	}
	_, _, err := splitYAMLKey(text)
	return err == nil
}

// blockScalar reads a | or > scalar: the lines indented deeper than the key.
func (p *yamlParser) blockScalar(header string, indent, num int) (*yamlNode, error) {
	style := header[0]
	for _, c := range header[1:] {
		if !strings.ContainsRune("+-0123456789", c) {
			if c == ' ' || c == '#' {
				break
			}
			return nil, &yamlError{num, "an unreadable block scalar header"}
		}
	}
	var parts []string
	for p.pos < len(p.lines) {
		l := p.lines[p.pos]
		if l.text != "" && l.indent <= indent {
			break
		}
		parts = append(parts, l.text)
		p.pos++
	}
	sep := "\n"
	if style == '>' {
		sep = " "
	}
	return &yamlNode{kind: yamlScalar, value: strings.TrimRight(strings.Join(parts, sep), " \n")}, nil
}

// splitYAMLKey splits "key: rest" or "key:" into the key and what follows.
func splitYAMLKey(text string) (key, rest string, err error) {
	switch text[0] {
	case '\'', '"':
		k, n, qerr := parseYAMLQuoted(text)
		if qerr != nil {
			return "", "", qerr
		}
		after := text[n:]
		if !strings.HasPrefix(after, ":") {
			return "", "", errors.New("a quoted key with no colon after it")
		}
		after = after[1:]
		if after != "" && after[0] != ' ' {
			return "", "", errors.New("a quoted key with no space after its colon")
		}
		return k, strings.TrimSpace(after), nil
	case '?', '&', '*', '!', '[', '{', '|', '>', ',':
		return "", "", errors.New("a key nvx does not read")
	}
	i := strings.Index(text, ": ")
	switch {
	case i >= 0:
		key, rest = text[:i], strings.TrimSpace(text[i+2:])
	case strings.HasSuffix(text, ":"):
		key = text[:len(text)-1]
	default:
		return "", "", errors.New("a line that is not a key and value")
	}
	key = strings.TrimSpace(key)
	if key == "" || strings.Contains(key, " #") {
		return "", "", errors.New("a key nvx does not read")
	}
	return key, rest, nil
}

// parseYAMLInline reads the value after a key or "- " on the same line.
func parseYAMLInline(s string, num int) (*yamlNode, error) {
	n, used, err := parseYAMLFlow(s, 0)
	if err != nil {
		return nil, &yamlError{num, err.Error()}
	}
	if tail := strings.TrimSpace(s[used:]); tail != "" && !strings.HasPrefix(tail, "#") {
		return nil, &yamlError{num, "text after a value"}
	}
	return n, nil
}

// yamlMaxFlowDepth bounds how deeply { } and [ ] nest. pnpm nests two deep.
const yamlMaxFlowDepth = 32

// parseYAMLFlow reads one value from the start of s and says how many bytes
// it used. depth counts the { } and [ ] around it, inside which , ] and } end
// a plain scalar.
func parseYAMLFlow(s string, depth int) (*yamlNode, int, error) {
	inFlow := depth > 0
	if depth > yamlMaxFlowDepth {
		return nil, 0, errors.New("flow collections nested too deeply")
	}
	if s == "" {
		return &yamlNode{kind: yamlNull}, 0, nil
	}
	switch s[0] {
	case '\'', '"':
		v, n, err := parseYAMLQuoted(s)
		return &yamlNode{kind: yamlScalar, value: v}, n, err
	case '{':
		return parseYAMLFlowMap(s, depth+1)
	case '[':
		return parseYAMLFlowSeq(s, depth+1)
	case '&', '*', '!':
		// An anchor, an alias or a tag, which change what a value means.
		return nil, 0, errors.New("a value nvx does not read")
	case '|', '>':
		if inFlow {
			return nil, 0, errors.New("a value nvx does not read")
		}
	}
	end := len(s)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inFlow && (c == ',' || c == ']' || c == '}') {
			end = i
			break
		}
		if c == '#' && i > 0 && s[i-1] == ' ' {
			end = i
			break
		}
		if inFlow && c == ':' && (i+1 == len(s) || s[i+1] == ' ' || s[i+1] == ',' || s[i+1] == '}') {
			end = i
			break
		}
	}
	v := strings.TrimSpace(s[:end])
	if v == "~" || v == "null" {
		return &yamlNode{kind: yamlNull}, end, nil
	}
	return &yamlNode{kind: yamlScalar, value: v}, end, nil
}

func parseYAMLFlowMap(s string, depth int) (*yamlNode, int, error) {
	n := &yamlNode{kind: yamlMap, fields: map[string]*yamlNode{}}
	i := 1
	for {
		i += countSpaces(s[i:])
		if i >= len(s) {
			return nil, 0, errors.New("a flow mapping that does not end on its line")
		}
		if s[i] == '}' {
			return n, i + 1, nil
		}
		keyNode, used, err := parseYAMLFlow(s[i:], depth)
		if err != nil {
			return nil, 0, err
		}
		if keyNode.kind != yamlScalar || keyNode.value == "" {
			return nil, 0, errors.New("a flow mapping key nvx does not read")
		}
		i += used
		i += countSpaces(s[i:])
		if i >= len(s) || s[i] != ':' {
			return nil, 0, errors.New("a flow mapping key with no value")
		}
		i++
		i += countSpaces(s[i:])
		val, used, err := parseYAMLFlow(s[i:], depth)
		if err != nil {
			return nil, 0, err
		}
		i += used
		if _, dup := n.fields[keyNode.value]; dup {
			return nil, 0, errors.New("a key that appears twice in one mapping")
		}
		n.keys = append(n.keys, keyNode.value)
		n.fields[keyNode.value] = val
		i += countSpaces(s[i:])
		if i < len(s) && s[i] == ',' {
			i++
			continue
		}
		if i < len(s) && s[i] == '}' {
			return n, i + 1, nil
		}
		return nil, 0, errors.New("a flow mapping nvx does not read")
	}
}

func parseYAMLFlowSeq(s string, depth int) (*yamlNode, int, error) {
	n := &yamlNode{kind: yamlSeq}
	i := 1
	for {
		i += countSpaces(s[i:])
		if i >= len(s) {
			return nil, 0, errors.New("a flow sequence that does not end on its line")
		}
		if s[i] == ']' {
			return n, i + 1, nil
		}
		val, used, err := parseYAMLFlow(s[i:], depth)
		if err != nil {
			return nil, 0, err
		}
		if used == 0 {
			return nil, 0, errors.New("a flow sequence nvx does not read")
		}
		i += used
		n.items = append(n.items, val)
		i += countSpaces(s[i:])
		if i < len(s) && s[i] == ',' {
			i++
			continue
		}
		if i < len(s) && s[i] == ']' {
			return n, i + 1, nil
		}
		return nil, 0, errors.New("a flow sequence nvx does not read")
	}
}

func countSpaces(s string) int {
	n := 0
	for n < len(s) && s[n] == ' ' {
		n++
	}
	return n
}

// parseYAMLQuoted reads a quoted scalar at the start of s, which must close on
// the same line, and returns its value and the bytes it used.
func parseYAMLQuoted(s string) (string, int, error) {
	q := s[0]
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		if q == '\'' {
			if c == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					b.WriteByte('\'')
					i++
					continue
				}
				return b.String(), i + 1, nil
			}
			b.WriteByte(c)
			continue
		}
		switch c {
		case '"':
			return b.String(), i + 1, nil
		case '\\':
			if i+1 >= len(s) {
				return "", 0, errors.New("a double-quoted string that does not end on its line")
			}
			i++
			r, n, err := yamlEscape(s[i:])
			if err != nil {
				return "", 0, err
			}
			b.WriteString(r)
			i += n - 1
		default:
			b.WriteByte(c)
		}
	}
	return "", 0, errors.New("a quoted string that does not end on its line")
}

// yamlEscape reads one escape after a backslash and returns its text and the
// bytes it used.
func yamlEscape(s string) (string, int, error) {
	simple := map[byte]string{'0': "\x00", 'a': "\a", 'b': "\b", 't': "\t", '\t': "\t", 'n': "\n", 'v': "\v",
		'f': "\f", 'r': "\r", 'e': "\x1b", ' ': " ", '"': `"`, '/': "/", '\\': `\`, 'N': "\u0085", '_': " ",
		'L': string(rune(0x2028)), 'P': string(rune(0x2029))}
	if r, ok := simple[s[0]]; ok {
		return r, 1, nil
	}
	width := map[byte]int{'x': 2, 'u': 4, 'U': 8}[s[0]]
	if width == 0 || len(s) < 1+width {
		return "", 0, errors.New("an escape nvx does not read")
	}
	v, err := strconv.ParseUint(s[1:1+width], 16, 32)
	if err != nil || !utf8.ValidRune(rune(v)) {
		return "", 0, errors.New("an escape nvx does not read")
	}
	return string(rune(v)), 1 + width, nil
}
