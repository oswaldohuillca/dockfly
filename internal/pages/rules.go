package pages

import (
	"bufio"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// A site's configuration files, as in Cloudflare Pages and Netlify.
const (
	redirectsFile = "_redirects"
	headersFile   = "_headers"
	maxRules      = 2000
)

// Rules are a deployment's redirects and headers.
type Rules struct {
	Redirects []RedirectRule
	Headers   []HeaderRule
}

type RedirectRule struct {
	from   *regexp.Regexp
	names  []string
	To     string
	Status int // 200 = rewrite (serves another file without changing the URL)
}

type HeaderRule struct {
	from   *regexp.Regexp
	Set    map[string]string
	Remove []string
}

// pattern turns "/blog/:slug/*" into an anchored regular expression that
// captures the parameters (:slug) and the wildcard (:splat).
func pattern(p string) (*regexp.Regexp, []string) {
	var b strings.Builder
	var names []string
	b.WriteString("^")
	for i := 0; i < len(p); i++ {
		switch c := p[i]; {
		case c == '*':
			b.WriteString("(.*)")
			names = append(names, "splat")
		case c == ':' && i+1 < len(p) && isNameChar(p[i+1]):
			j := i + 1
			for j < len(p) && isNameChar(p[j]) {
				j++
			}
			b.WriteString("([^/]+)")
			names = append(names, p[i+1:j])
			i = j - 1
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String()), names
}

func isNameChar(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// ParseRedirects reads a _redirects file: "from to [code]" per line.
func ParseRedirects(r io.Reader) []RedirectRule {
	var out []RedirectRule
	sc := bufio.NewScanner(r)
	for sc.Scan() && len(out) < maxRules {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 || !strings.HasPrefix(f[0], "/") {
			continue
		}
		status := http.StatusFound // as in Cloudflare Pages: 302 by default
		if len(f) >= 3 {
			n, err := strconv.Atoi(strings.TrimSuffix(f[2], "!"))
			if err != nil {
				continue
			}
			status = n
		}
		switch status {
		case 200, 301, 302, 303, 307, 308:
		default:
			continue
		}
		// Some browsers treat protocol-relative targets ("//host") or ones with
		// "\" as external → open redirect. They are rejected.
		if strings.ContainsRune(f[1], '\\') || strings.HasPrefix(f[1], "//") {
			continue
		}
		// A rewrite can only point at another path of the site itself.
		if status == 200 && !strings.HasPrefix(f[1], "/") {
			continue
		}
		re, names := pattern(f[0])
		out = append(out, RedirectRule{from: re, names: names, To: f[1], Status: status})
	}
	return out
}

// ParseHeaders reads a _headers file: a path and, indented below it, its
// headers ("Name: value", or "! Name" to remove one).
// forbiddenHeader lists the headers a _headers file cannot set.
var forbiddenHeader = map[string]bool{"Set-Cookie": true}

func ParseHeaders(r io.Reader) []HeaderRule {
	var out []HeaderRule
	var cur *HeaderRule
	sc := bufio.NewScanner(r)
	for sc.Scan() && len(out) <= maxRules {
		raw := sc.Text()
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		indented := raw[0] == ' ' || raw[0] == '\t'
		if !indented {
			if !strings.HasPrefix(line, "/") {
				cur = nil
				continue
			}
			re, _ := pattern(line)
			out = append(out, HeaderRule{from: re, Set: map[string]string{}})
			cur = &out[len(out)-1]
			continue
		}
		if cur == nil {
			continue
		}
		if name, ok := strings.CutPrefix(line, "!"); ok {
			cur.Remove = append(cur.Remove, http.CanonicalHeaderKey(strings.TrimSpace(name)))
			continue
		}
		if name, value, ok := strings.Cut(line, ":"); ok {
			key := http.CanonicalHeaderKey(strings.TrimSpace(name))
			// Set-Cookie would allow planting cookies on the parent domain
			// (cookie tossing against the panel if they share a domain).
			if key != "" && !forbiddenHeader[key] {
				cur.Set[key] = strings.TrimSpace(value)
			}
		}
	}
	return out
}

// Match looks for the first redirect matching path and returns its target
// with the parameters substituted.
func (r *Rules) Match(path string) (to string, status int, ok bool) {
	for _, rule := range r.Redirects {
		m := rule.from.FindStringSubmatch(path)
		if m == nil {
			continue
		}
		to = rule.To
		for i, name := range rule.names {
			to = strings.ReplaceAll(to, ":"+name, m[i+1])
		}
		return to, rule.Status, true
	}
	return "", 0, false
}

// ApplyHeaders adds (or removes) the headers of the rules that match.
func (r *Rules) ApplyHeaders(h http.Header, path string) {
	for _, rule := range r.Headers {
		if !rule.from.MatchString(path) {
			continue
		}
		for k, v := range rule.Set {
			h.Set(k, v)
		}
		for _, k := range rule.Remove {
			h.Del(k)
		}
	}
}

// rulesCache keeps each deployment's rules: they are immutable, so they are
// read only once.
var rulesCache sync.Map // dir → *Rules

func loadRules(dir string) *Rules {
	if r, ok := rulesCache.Load(dir); ok {
		return r.(*Rules)
	}
	rules := &Rules{}
	if f, err := os.Open(dir + "/" + redirectsFile); err == nil {
		rules.Redirects = ParseRedirects(io.LimitReader(f, 1<<20))
		f.Close()
	}
	if f, err := os.Open(dir + "/" + headersFile); err == nil {
		rules.Headers = ParseHeaders(io.LimitReader(f, 1<<20))
		f.Close()
	}
	rulesCache.Store(dir, rules)
	return rules
}
