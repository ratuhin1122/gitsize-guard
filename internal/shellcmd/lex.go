package shellcmd

import (
	"strings"
)

// word is one shell word after quote removal.
type word struct {
	text    string
	dynamic bool   // contains an expansion the shell rewrites: $..., $(...), `...`, {a,b}
	subst   string // for a word that is exactly one $(...): its body
}

// simpleCmd is one command between control operators, or a subshell
// boundary marker.
type simpleCmd struct {
	words     []word
	push, pop bool        // "(" and ")"
	writes    []word      // files output is redirected to (not /dev/... or a file descriptor)
	stdin     []stdinText // heredoc bodies and here-strings fed to the command
	subs      []string    // command/process substitutions, which run before the command
}

// stdinText is text fed to a command's stdin.
type stdinText struct {
	text    string
	expands bool // the shell expands $, ` and \ in it first (unquoted heredoc, dynamic here-string)
}

type heredoc struct {
	delim     string
	stripTabs bool
	quoted    bool // <<'EOF': the body is not expanded
	owner     int  // index in cmds of the command the heredoc belongs to
}

// lexer is a best-effort POSIX shell tokenizer. It understands quoting
// (including $'...'), escapes, comments, redirections, heredocs, subshells,
// arithmetic, brace expansion, and command/process substitution, which is
// enough to find git invocations in the commands Claude Code writes. It does
// not expand anything except a leading ~.
type lexer struct {
	src  []rune
	home string

	ok   bool        // false if the line could not be tokenized completely
	cmds []simpleCmd // top-level commands in order
	subs []string    // bodies of command/process substitutions

	cur                  []word
	buf                  strings.Builder
	inWord, dyn, tilde   bool
	braceOpen, braceSep  bool
	wordSubs             []string // substitutions seen in the current word
	redirTarget          bool     // the next word is a redirection target, not an argument
	redirOut, hereString bool     // ...of an output redirection / a here-string
	curWrites            []word
	curStdin             []stdinText
	curSubs              []string
	pending              []heredoc
}

func lex(src, home string) *lexer {
	l := &lexer{src: []rune(src), home: home, ok: true}
	l.run()
	return l
}

func (l *lexer) run() {
	s := l.src
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\':
			if i+1 < len(s) {
				i++
				if s[i] != '\n' { // backslash-newline is a line continuation
					l.buf.WriteRune(s[i])
					l.inWord = true
				}
			}
		case c == '\'':
			l.inWord = true
			j := indexRune(s, i+1, '\'')
			if j < 0 {
				l.ok = false
				j = len(s)
			}
			l.buf.WriteString(string(s[i+1 : min(j, len(s))]))
			i = j
		case c == '"':
			l.inWord = true
			i = l.readDouble(i + 1)
		case c == '`':
			l.inWord, l.dyn = true, true
			i = l.readBacktick(i + 1)
		case c == '$':
			l.inWord = true
			i = l.readDollar(i, false)
		case c == '#' && !l.inWord:
			for i+1 < len(s) && s[i+1] != '\n' {
				i++
			}
		case c == '\n':
			l.flushCmd()
			i = l.skipHeredocBodies(i + 1)
		case c == '&' && i+1 < len(s) && s[i+1] == '>': // &> file, &>> file
			i = l.readRedirect(i + 1)
		case c == ';' || c == '&' || c == '|':
			l.flushCmd()
		case c == '(' && l.inWord && strings.HasSuffix(l.buf.String(), "="):
			// Array assignment: name=(a b "$(cmd)"). Not a subshell.
			body, end := l.scanParens(i+1, false)
			inner := substitutions(body)
			l.subs = append(l.subs, inner...)
			l.curSubs = append(l.curSubs, inner...)
			l.buf.WriteString("(...)")
			l.dyn = true
			i = end
		case c == '(' && !l.inWord && i+1 < len(s) && s[i+1] == '(':
			// Arithmetic (( ... )): no commands inside, and "<<" is a shift.
			l.flushWord()
			_, i = l.scanParens(i+1, true)
		case c == '(':
			l.flushCmd()
			l.cmds = append(l.cmds, simpleCmd{push: true})
		case c == ')':
			l.flushCmd()
			l.cmds = append(l.cmds, simpleCmd{pop: true})
		case c == '<' || c == '>':
			i = l.readRedirect(i)
		case c == ' ' || c == '\t': // bash splits only on these (and newline)
			l.flushWord()
		default:
			switch {
			case c == '~' && !l.inWord:
				l.tilde = true
			case c == '{':
				l.braceOpen = true
			case l.braceOpen && (c == ',' || (c == '.' && i+1 < len(s) && s[i+1] == '.')):
				l.braceSep = true
			case c == '}' && l.braceSep:
				l.dyn = true // brace expansion: the shell turns this word into several
			}
			l.buf.WriteRune(c)
			l.inWord = true
		}
	}
	l.flushCmd()
}

// readDouble consumes a double-quoted string starting after the opening
// quote and returns the index of the closing quote.
func (l *lexer) readDouble(i int) int {
	s := l.src
	for ; i < len(s); i++ {
		switch c := s[i]; c {
		case '"':
			return i
		case '\\':
			if i+1 < len(s) && strings.ContainsRune("$`\"\\\n", s[i+1]) {
				i++
				if s[i] != '\n' {
					l.buf.WriteRune(s[i])
				}
			} else {
				l.buf.WriteRune(c)
			}
		case '`':
			l.dyn = true
			i = l.readBacktick(i + 1)
		case '$':
			i = l.readDollar(i, true)
		default:
			l.buf.WriteRune(c)
		}
	}
	l.ok = false
	return i
}

// readDollar handles $name, ${...}, $'...', $(...) and $((...)) at
// s[i] == '$' and returns the index of the last rune consumed.
func (l *lexer) readDollar(i int, inDouble bool) int {
	s := l.src
	if !inDouble && i+1 < len(s) && s[i+1] == '\'' { // ANSI-C quoting: literal text
		j := i + 2
		for ; j < len(s) && s[j] != '\''; j++ {
			if s[j] == '\\' && j+1 < len(s) {
				j++
				l.buf.WriteRune(ansiEscape(s[j]))
				continue
			}
			l.buf.WriteRune(s[j])
		}
		if j >= len(s) {
			l.ok = false
		}
		return j
	}

	l.dyn = true
	l.buf.WriteRune('$')
	if i+1 >= len(s) {
		return i
	}
	switch s[i+1] {
	case '(':
		arith := i+2 < len(s) && s[i+2] == '('
		body, end := l.scanParens(i+2, arith)
		if !arith {
			l.subs = append(l.subs, body)
			l.curSubs = append(l.curSubs, body)
			l.wordSubs = append(l.wordSubs, body)
		}
		return end
	case '{':
		j := indexRune(s, i+2, '}')
		if j < 0 {
			l.ok = false
			return len(s)
		}
		return j
	}
	return i
}

// readBacktick consumes a `...` substitution starting after the opening
// backtick and returns the index of the closing one.
func (l *lexer) readBacktick(i int) int {
	s := l.src
	var body strings.Builder
	for ; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 < len(s) {
				i++
				body.WriteRune(s[i])
			}
		case '`':
			l.subs = append(l.subs, body.String())
			l.curSubs = append(l.curSubs, body.String())
			l.wordSubs = append(l.wordSubs, body.String())
			return i
		default:
			body.WriteRune(s[i])
		}
	}
	l.ok = false
	return i
}

// scanParens scans a $( ... ), <( ... ) or (( ... )) body starting just after
// the "(", honouring nested parentheses, quotes, comments and heredocs (not
// in arithmetic, where "<<" is a shift). It returns the body and the index of
// the matching ")".
func (l *lexer) scanParens(i int, arith bool) (string, int) {
	s := l.src
	start, depth := i, 1
	var pending []heredoc
	for ; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			i++
		case '\'':
			ansi := i > 0 && s[i-1] == '$'
			for i++; i < len(s) && s[i] != '\''; i++ {
				if ansi && s[i] == '\\' {
					i++
				}
			}
		case '"':
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' {
					i++
				}
			}
		case '#':
			if !arith && (i == start || strings.ContainsRune(" \t\n;&|(", s[i-1])) {
				for i+1 < len(s) && s[i+1] != '\n' {
					i++
				}
			}
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return string(s[start:i]), i
			}
		case '<':
			if arith {
				continue
			}
			switch {
			case i+2 < len(s) && s[i+1] == '<' && s[i+2] == '<': // here-string
				i += 2
			case i+1 < len(s) && s[i+1] == '<':
				h, end := readHeredocDelim(s, i+2)
				pending = append(pending, h)
				i = end - 1
			}
		case '\n':
			if len(pending) > 0 {
				end, _ := skipBodies(s, i+1, pending)
				i = end - 1
				pending = nil
			}
		}
	}
	l.ok = false
	return string(s[start:]), len(s)
}

// readRedirect handles <, >, >>, >&, <<, <<<, <( and >( at s[i] and returns
// the index of the last rune consumed.
func (l *lexer) readRedirect(i int) int {
	s := l.src
	// A word made only of digits right before the operator is a file descriptor.
	if l.inWord && !l.dyn && isDigits(l.buf.String()) {
		l.buf.Reset()
		l.inWord = false
	}
	l.flushWord()
	c := s[i]
	if i+1 < len(s) && s[i+1] == '(' { // process substitution
		body, end := l.scanParens(i+2, false)
		l.subs = append(l.subs, body)
		l.curSubs = append(l.curSubs, body)
		l.cur = append(l.cur, word{text: string(c) + "(...)", dynamic: true})
		return end
	}
	if c == '<' && i+1 < len(s) && s[i+1] == '<' {
		if i+2 < len(s) && s[i+2] == '<' { // here-string: <<< word
			l.redirTarget, l.hereString = true, true
			return i + 2
		}
		h, end := readHeredocDelim(s, i+2)
		h.owner = len(l.cmds) // index the current command gets when flushed
		l.pending = append(l.pending, h)
		return end - 1
	}
	out := c == '>'
	for i+1 < len(s) && strings.ContainsRune("<>&|", s[i+1]) {
		i++
		out = out || s[i] == '>'
	}
	l.redirTarget, l.redirOut = true, out
	return i
}

// skipHeredocBodies skips the bodies of heredocs opened on the line that just
// ended, attaching them to their commands, and returns the index before the
// next rune to lex.
func (l *lexer) skipHeredocBodies(i int) int {
	if len(l.pending) == 0 {
		return i - 1
	}
	end, bodies := skipBodies(l.src, i, l.pending)
	for k, h := range l.pending {
		if h.owner < len(l.cmds) {
			l.cmds[h.owner].stdin = append(l.cmds[h.owner].stdin, stdinText{text: bodies[k], expands: !h.quoted})
		}
		if !h.quoted && h.owner < len(l.cmds) { // unquoted bodies run their $(...) and `...`
			inner := substitutions(bodies[k])
			l.subs = append(l.subs, inner...)
			l.cmds[h.owner].subs = append(l.cmds[h.owner].subs, inner...)
		}
	}
	l.pending = nil
	return end - 1
}

func (l *lexer) flushWord() {
	if !l.inWord {
		return
	}
	t := l.buf.String()
	if l.tilde && l.home != "" && (t == "~" || strings.HasPrefix(t, "~/")) {
		t = l.home + t[1:]
	}
	w := word{text: t, dynamic: l.dyn}
	if t == "$" && len(l.wordSubs) == 1 {
		w.subst = l.wordSubs[0]
	}
	if l.redirTarget {
		switch {
		case l.hereString:
			l.curStdin = append(l.curStdin, stdinText{text: t, expands: l.dyn})
		case l.redirOut && !isDigits(t) && t != "-" && !strings.HasPrefix(t, "/dev/"):
			l.curWrites = append(l.curWrites, w)
		}
		l.redirTarget, l.redirOut, l.hereString = false, false, false
	} else {
		l.cur = append(l.cur, w)
	}
	l.buf.Reset()
	l.inWord, l.dyn, l.tilde, l.braceOpen, l.braceSep = false, false, false, false, false
	l.wordSubs = nil
}

func (l *lexer) flushCmd() {
	l.flushWord()
	l.redirTarget, l.redirOut, l.hereString = false, false, false
	// Keep redirect-only commands too, as after "}" or ")": "{ ...; } > file".
	if len(l.cur) > 0 || len(l.curWrites) > 0 || len(l.curSubs) > 0 {
		l.cmds = append(l.cmds, simpleCmd{words: l.cur, writes: l.curWrites, stdin: l.curStdin, subs: l.curSubs})
	}
	l.cur, l.curWrites, l.curStdin, l.curSubs = nil, nil, nil, nil
}

// substitutions returns the bodies of $(...) and `...` in text that the shell
// expands like a double-quoted string (e.g. an unquoted heredoc body).
func substitutions(text string) []string {
	l := &lexer{src: []rune(text), ok: true}
	s := l.src
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '`':
			i = l.readBacktick(i + 1)
		case '$':
			if i+1 < len(s) && s[i+1] == '(' {
				arith := i+2 < len(s) && s[i+2] == '('
				body, end := l.scanParens(i+2, arith)
				if !arith {
					l.subs = append(l.subs, body)
				}
				i = end
			}
		}
	}
	return l.subs
}

// readHeredocDelim parses the delimiter after "<<" (with optional "-") and
// returns it with the index just past it.
func readHeredocDelim(s []rune, i int) (heredoc, int) {
	var h heredoc
	if i < len(s) && s[i] == '-' {
		h.stripTabs = true
		i++
	}
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	var b strings.Builder
	for ; i < len(s); i++ {
		c := s[i]
		if c == '\'' || c == '"' {
			h.quoted = true
			j := indexRune(s, i+1, c)
			if j < 0 {
				j = len(s)
			}
			b.WriteString(string(s[i+1 : min(j, len(s))]))
			i = j
			continue
		}
		if c == '\\' && i+1 < len(s) {
			h.quoted = true
			i++
			b.WriteRune(s[i])
			continue
		}
		if c == ' ' || c == '\t' || c == '\n' || strings.ContainsRune(";&|<>()", c) {
			break
		}
		b.WriteRune(c)
	}
	h.delim = b.String()
	return h, i
}

// skipBodies skips heredoc bodies starting at i (the start of the line after
// the redirection). It returns the index of the first rune after the last
// delimiter line, and each body's text.
func skipBodies(s []rune, i int, docs []heredoc) (int, []string) {
	bodies := make([]string, len(docs))
	for k, h := range docs {
		var b strings.Builder
		for i < len(s) {
			end := indexRune(s, i, '\n')
			if end < 0 {
				end = len(s)
			}
			line := string(s[i:end])
			i = end + 1
			cmp := line
			if h.stripTabs {
				cmp = strings.TrimLeft(line, "\t")
			}
			if cmp == h.delim {
				break
			}
			b.WriteString(line)
			b.WriteByte('\n')
		}
		bodies[k] = b.String()
	}
	return min(i, len(s)), bodies
}

func ansiEscape(r rune) rune {
	switch r {
	case 'n':
		return '\n'
	case 't':
		return '\t'
	case 'r':
		return '\r'
	}
	return r
}

func indexRune(s []rune, from int, r rune) int {
	for i := from; i < len(s); i++ {
		if s[i] == r {
			return i
		}
	}
	return -1
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
