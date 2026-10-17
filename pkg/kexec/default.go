package kexec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ErrUnsupported marks a boot configuration that BOOTy can not resolve the
// way GRUB would. The caller reboots through the firmware instead of
// starting a kernel that GRUB would not start.
var ErrUnsupported = errors.New("boot entry can not be resolved without GRUB")

// grubConfigPaths are the GRUB configurations below a root, in order:
// Debian and Ubuntu, then RHEL-likes and SUSE.
var grubConfigPaths = []string{filepath.Join("boot", "grub", "grub.cfg"), filepath.Join("boot", "grub2", "grub.cfg")}

// cosmeticVars are GRUB variables that only change the console handover.
// GRUB sets them in functions, so BOOTy can not evaluate them; an empty
// value is what GRUB uses in text mode.
var cosmeticVars = map[string]bool{"vt_handoff": true}

// DefaultEntry returns the entry that GRUB boots by default from the
// installed root: the grub.cfg of boot/grub or boot/grub2, the default of
// grub.cfg, next_entry and saved_entry of grubenv, and submenu paths
// ("1>2" or titles and ids). Variables in the kernel line are expanded when
// grub.cfg sets them unconditionally. Every case that needs GRUB script
// evaluation (BLS entries, conditional kernel lines, variables set in
// functions or conditions, several initrds) returns ErrUnsupported.
func DefaultEntry(root string) (*BootEntry, error) {
	cfgPath, data, err := readGrubConfig(root)
	if err != nil {
		return nil, err
	}
	cfg := parseGrubScript(string(data))
	if cfg.bls {
		return nil, fmt.Errorf("%w: %s uses BLS entries (blscfg)", ErrUnsupported, cfgPath)
	}
	env := readGrubenv(filepath.Join(filepath.Dir(cfgPath), "grubenv"))
	spec, err := cfg.defaultSpec(env)
	if err != nil {
		return nil, err
	}
	item, err := cfg.resolve(spec)
	if err != nil {
		return nil, err
	}
	return cfg.bootEntry(item)
}

func readGrubConfig(root string) (path string, data []byte, err error) {
	for _, rel := range grubConfigPaths {
		p := filepath.Join(root, rel)
		data, err = os.ReadFile(p)
		if err == nil {
			return p, data, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", nil, fmt.Errorf("read %s: %w", p, err)
		}
	}
	if entries, _ := filepath.Glob(filepath.Join(root, "boot", "loader", "entries", "*.conf")); len(entries) > 0 {
		return "", nil, fmt.Errorf("%w: BLS entries without grub.cfg", ErrUnsupported)
	}
	return "", nil, fmt.Errorf("no grub.cfg in %s", strings.Join(grubConfigPaths, " or "))
}

// grubItem is a menuentry or a submenu.
type grubItem struct {
	title, id string
	submenu   bool
	children  []*grubItem
	linux     [][]string // the words after each linux command
	initrd    [][]string
	badLinux  string // a linux command with a variable name, like linux$suf
	// conditional is an item inside an if or function block: GRUB may or
	// may not add it, so the numbers of the items after it are unsure.
	conditional bool
}

type grubScript struct {
	items    []*grubItem
	defaults []string          // values of "set default=" outside entries
	vars     map[string]string // variables set once, outside blocks
	unsure   map[string]bool   // variables set in blocks or more than once
	bls      bool
}

type block int

const (
	blockMenu block = iota
	blockSubmenu
	blockOther // function, if
)

// parseGrubScript reads the structure of a grub.cfg: entries, submenus,
// "set" commands and their nesting. It does not run the script.
func parseGrubScript(text string) *grubScript {
	s := &grubScript{vars: map[string]string{}, unsure: map[string]bool{}}
	var stack []block
	var menus []*grubItem // open menuentry and submenu items
	for _, raw := range strings.Split(text, "\n") {
		words := shellWords(strings.TrimSpace(raw))
		if len(words) == 0 || strings.HasPrefix(words[0], "#") {
			continue
		}
		switch words[0] {
		case "menuentry", "submenu":
			item := newGrubItem(words)
			item.conditional = slices.Contains(stack, blockOther)
			if len(menus) > 0 {
				parent := menus[len(menus)-1]
				parent.children = append(parent.children, item)
			} else {
				s.items = append(s.items, item)
			}
			menus = append(menus, item)
			kind := blockMenu
			if item.submenu {
				kind = blockSubmenu
			}
			stack = append(stack, kind)
			continue
		case "}":
			if n := len(stack); n > 0 {
				if stack[n-1] != blockOther {
					menus = menus[:len(menus)-1]
				}
				stack = stack[:n-1]
			}
			continue
		}
		s.command(words, stack, menus)
		stack = nestBlocks(words, stack)
	}
	return s
}

// nestBlocks applies the if, while and function openings and the fi and
// done closings of one line to the block stack. Keywords count only in
// command position: first word, or after a word that ends with ";", or
// after then, else, do.
func nestBlocks(words []string, stack []block) []block {
	cmdPos := true
	for _, w := range words {
		word := strings.TrimSuffix(w, ";")
		if cmdPos {
			switch word {
			case "if", "while", "until", "function":
				stack = append(stack, blockOther)
			case "fi", "done":
				if n := len(stack); n > 0 && stack[n-1] == blockOther {
					stack = stack[:n-1]
				}
			}
		}
		cmdPos = strings.HasSuffix(w, ";") || word == "then" || word == "else" || word == "do"
	}
	return stack
}

func (s *grubScript) command(words []string, stack []block, menus []*grubItem) {
	inEntry := len(menus) > 0 && !menus[len(menus)-1].submenu
	switch {
	case words[0] == "blscfg":
		s.bls = true
	case inEntry:
		item := menus[len(menus)-1]
		switch words[0] {
		case "linux", "linux16", "linuxefi":
			item.linux = append(item.linux, words[1:])
		case "initrd", "initrd16", "initrdefi":
			item.initrd = append(item.initrd, words[1:])
		default:
			if strings.HasPrefix(words[0], "linux$") || strings.HasPrefix(words[0], "linux${") {
				item.badLinux = words[0]
			}
		}
	case len(menus) == 0:
		s.assignment(words, len(stack) > 0)
	}
}

// assignment records "set name=value" and "name=value" outside entries.
func (s *grubScript) assignment(words []string, inBlock bool) {
	w := words
	if w[0] == "set" && len(w) > 1 {
		w = w[1:]
	}
	name, value, ok := strings.Cut(w[0], "=")
	if !ok || name == "" || len(w) > 1 && words[0] != "set" {
		return
	}
	if name == "default" {
		s.defaults = append(s.defaults, value)
		return
	}
	if _, seen := s.vars[name]; seen || inBlock {
		s.unsure[name] = true
		return
	}
	s.vars[name] = value
}

func newGrubItem(words []string) *grubItem {
	item := &grubItem{submenu: words[0] == "submenu"}
	for i := 1; i < len(words); i++ {
		w := words[i]
		switch {
		case w == "{":
		case item.title == "" && !strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "$"):
			item.title = w
		case strings.HasPrefix(w, "--id="):
			item.id = strings.TrimPrefix(w, "--id=")
		case (w == "--id" || w == "$menuentry_id_option" || w == "${menuentry_id_option}") && i+1 < len(words):
			item.id = words[i+1]
			i++
		}
	}
	return item
}

// defaultSpec is the default that GRUB uses: next_entry of grubenv when
// set (Debian and Ubuntu grub.cfg read it first), else the "set default"
// of grub.cfg with ${saved_entry} from grubenv. An empty default is "0".
func (s *grubScript) defaultSpec(env map[string]string) (string, error) {
	if env["next_entry"] != "" {
		return env["next_entry"], nil
	}
	saved := env["saved_entry"]
	if env["prev_saved_entry"] != "" {
		saved = env["prev_saved_entry"]
	}
	spec := "0"
	for _, d := range s.defaults {
		switch d {
		case "${next_entry}", "$next_entry":
		case "${saved_entry}", "$saved_entry":
			spec = saved
		default:
			if strings.Contains(d, "$") {
				return "", fmt.Errorf("%w: default %q", ErrUnsupported, d)
			}
			spec = d
		}
	}
	if spec == "" {
		spec = "0"
	}
	return spec, nil
}

// resolve finds the item of a default like "0", "1>2", a title or an id,
// or "gnulinux-advanced-x>gnulinux-5.15-advanced-x".
func (s *grubScript) resolve(spec string) (*grubItem, error) {
	items := s.items
	var item *grubItem
	for _, part := range strings.Split(spec, ">") {
		item = findItem(items, part)
		if item == nil {
			return nil, fmt.Errorf("%w: no entry %q of default %q", ErrUnsupported, part, spec)
		}
		items = item.children
	}
	if item == nil || item.submenu {
		return nil, fmt.Errorf("%w: default %q is a submenu", ErrUnsupported, spec)
	}
	return item, nil
}

func findItem(items []*grubItem, part string) *grubItem {
	if n, err := strconv.Atoi(part); err == nil {
		if n < 0 || n >= len(items) {
			return nil
		}
		for _, it := range items[:n+1] {
			if it.conditional {
				return nil // the number depends on a condition
			}
		}
		return items[n]
	}
	for _, it := range items {
		if it.id == part {
			return it
		}
	}
	for _, it := range items {
		if it.title == part {
			return it
		}
	}
	return nil
}

func (s *grubScript) bootEntry(item *grubItem) (*BootEntry, error) {
	switch {
	case item.badLinux != "":
		return nil, fmt.Errorf("%w: entry %q loads the kernel with %q", ErrUnsupported, item.title, item.badLinux)
	case len(item.linux) != 1 || len(item.linux[0]) == 0:
		return nil, fmt.Errorf("%w: entry %q has %d linux commands (conditional or none)", ErrUnsupported, item.title, len(item.linux))
	case len(item.initrd) > 1 || len(item.initrd) == 1 && len(item.initrd[0]) != 1:
		return nil, fmt.Errorf("%w: entry %q has several initrds", ErrUnsupported, item.title)
	}
	words, err := s.expandWords(item.linux[0])
	if err != nil {
		return nil, fmt.Errorf("entry %q: %w", item.title, err)
	}
	if len(words) == 0 {
		return nil, fmt.Errorf("%w: entry %q has no kernel", ErrUnsupported, item.title)
	}
	entry := &BootEntry{Name: item.title, Kernel: words[0], KernelArgs: strings.Join(words[1:], " ")}
	if len(item.initrd) == 1 {
		initrd, err := s.expandWords(item.initrd[0])
		if err != nil || len(initrd) != 1 {
			return nil, fmt.Errorf("%w: entry %q: initrd %v", ErrUnsupported, item.title, item.initrd[0])
		}
		entry.Initramfs = initrd[0]
	}
	return entry, nil
}

var varRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// expandWords expands $name and ${name} like GRUB, for variables that
// grub.cfg sets once outside blocks; a cosmetic variable becomes empty.
// Any other variable returns ErrUnsupported. Empty words are dropped.
func (s *grubScript) expandWords(words []string) ([]string, error) {
	var out []string
	for _, w := range words {
		v, err := s.expand(w, 0)
		if err != nil {
			return nil, err
		}
		out = append(out, strings.Fields(v)...)
	}
	return out, nil
}

func (s *grubScript) expand(w string, depth int) (string, error) {
	if depth > 5 {
		return "", fmt.Errorf("%w: variables nest too deep in %q", ErrUnsupported, w)
	}
	var firstErr error
	out := varRE.ReplaceAllStringFunc(w, func(m string) string {
		sub := varRE.FindStringSubmatch(m)
		name := sub[1] + sub[2]
		value, ok := s.vars[name]
		switch {
		case cosmeticVars[name]:
			return ""
		case !ok || s.unsure[name]:
			if firstErr == nil {
				firstErr = fmt.Errorf("%w: variable $%s is not set unconditionally in grub.cfg", ErrUnsupported, name)
			}
			return m
		}
		v, err := s.expand(value, depth+1)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		return v
	})
	return out, firstErr
}

// readGrubenv reads a GRUB environment block: name=value lines, "#"
// padding and comments. A missing file is an empty environment.
func readGrubenv(path string) map[string]string {
	env := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return env
	}
	for _, line := range strings.Split(string(data), "\n") {
		if name, value, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") {
			env[strings.TrimSpace(name)] = strings.TrimSpace(value)
		}
	}
	return env
}

// shellWords splits a GRUB script line into words. Quotes group words and
// are removed; a ";" at the end of a word stays part of it.
func shellWords(line string) []string {
	var words []string
	var cur strings.Builder
	var quote rune
	inWord := false
	for _, r := range line {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words
}
