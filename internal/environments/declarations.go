package environments

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Declarations is the small, static subset of Compose declarations used to warn
// about host resources that environment aliases would share. It is not a
// Compose merge or execution model: variable interpolation, !reset, extends and
// merge keys are not evaluated, and the Compose CLI remains the authority.
type Declarations struct {
	// ProjectName is the last top-level `name:` declared by the file list.
	ProjectName string
	Ports       []Port
	// Binds are absolute (or ~-prefixed) host bind sources.
	Binds            []Bind
	ExternalVolumes  []string
	ExternalNetworks []string
}

// Bind is one absolute host bind source declared by a service.
type Bind struct {
	Service string
	Source  string
}

// Port is one published host port declaration.
type Port struct {
	Service   string
	HostIP    string
	Published string
	Protocol  string
}

// Conflict is one host resource declared by both environments.
type Conflict struct {
	Kind     string // port, bind, external_volume, external_network
	Resource string
	Service  string
	Other    string
}

// ReadDeclarations statically reads the given Compose files in order. Missing
// or unreadable files are reported as an error so the caller can skip the alias.
func ReadDeclarations(files []string) (Declarations, error) {
	var out Declarations
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return Declarations{}, err
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return Declarations{}, fmt.Errorf("read %s: %w", path, err)
		}
		if len(doc.Content) == 0 {
			continue
		}
		root := doc.Content[0]
		if root.Kind != yaml.MappingNode {
			continue
		}
		parseDoc(root, &out)
	}
	return out, nil
}

type scanDoc struct {
	Name     string                 `yaml:"name"`
	Services map[string]scanService `yaml:"services"`
	Volumes  map[string]scanScope   `yaml:"volumes"`
	Networks map[string]scanScope   `yaml:"networks"`
}

type scanService struct {
	Ports   []yaml.Node `yaml:"ports"`
	Volumes []yaml.Node `yaml:"volumes"`
}

type scanScope struct {
	External bool   `yaml:"external"`
	Name     string `yaml:"name"`
}

func parseDoc(root *yaml.Node, out *Declarations) {
	var doc scanDoc
	if err := root.Decode(&doc); err != nil {
		return
	}
	if name := strings.TrimSpace(doc.Name); name != "" {
		out.ProjectName = name
	}
	services := make([]string, 0, len(doc.Services))
	for name := range doc.Services {
		services = append(services, name)
	}
	sort.Strings(services)
	for _, name := range services {
		svc := doc.Services[name]
		for i := range svc.Ports {
			if p, ok := parsePort(name, &svc.Ports[i]); ok {
				out.Ports = append(out.Ports, p)
			}
		}
		for i := range svc.Volumes {
			if src, ok := parseBindSource(&svc.Volumes[i]); ok {
				out.Binds = append(out.Binds, Bind{Service: name, Source: src})
			}
		}
	}
	volumes := make([]string, 0, len(doc.Volumes))
	for name := range doc.Volumes {
		volumes = append(volumes, name)
	}
	sort.Strings(volumes)
	for _, key := range volumes {
		scope := doc.Volumes[key]
		if !scope.External {
			continue
		}
		name := scope.Name
		if name == "" {
			name = key
		}
		out.ExternalVolumes = append(out.ExternalVolumes, name)
	}
	networks := make([]string, 0, len(doc.Networks))
	for name := range doc.Networks {
		networks = append(networks, name)
	}
	sort.Strings(networks)
	for _, key := range networks {
		scope := doc.Networks[key]
		if !scope.External {
			continue
		}
		name := scope.Name
		if name == "" {
			name = key
		}
		out.ExternalNetworks = append(out.ExternalNetworks, name)
	}
}

var (
	publishedPortRe = regexp.MustCompile(`^\d+(-\d+)?$`)
)

func parsePort(service string, node *yaml.Node) (Port, bool) {
	node = resolveAlias(node)
	if node == nil {
		return Port{}, false
	}
	switch node.Kind {
	case yaml.ScalarNode:
		return parseShortPort(service, node.Value)
	case yaml.MappingNode:
		var raw struct {
			HostIP    string    `yaml:"host_ip"`
			Published yaml.Node `yaml:"published"`
			Protocol  string    `yaml:"protocol"`
		}
		if err := node.Decode(&raw); err != nil {
			return Port{}, false
		}
		published := strings.TrimSpace(raw.Published.Value)
		if published == "" {
			return Port{}, false
		}
		if _, err := strconv.Atoi(published); err != nil && !publishedPortRe.MatchString(published) {
			return Port{}, false
		}
		protocol := strings.ToLower(strings.TrimSpace(raw.Protocol))
		if protocol == "" {
			protocol = "tcp"
		}
		return Port{Service: service, HostIP: strings.TrimSpace(raw.HostIP), Published: published, Protocol: protocol}, true
	default:
		return Port{}, false
	}
}

func parseShortPort(service, raw string) (Port, bool) {
	value := strings.TrimSpace(raw)
	if value == "" || strings.Contains(value, "$") {
		return Port{}, false
	}
	protocol := "tcp"
	if base, proto, ok := strings.Cut(value, "/"); ok {
		value = base
		protocol = strings.ToLower(strings.TrimSpace(proto))
		if protocol == "" {
			protocol = "tcp"
		}
	}
	if strings.Contains(value, "[") {
		// Bracketed IPv6 short syntax is not parsed; Compose still owns the
		// real interpretation and the host port is not guessed.
		return Port{}, false
	}
	parts := strings.Split(value, ":")
	var hostIP, published string
	switch len(parts) {
	case 2:
		published = parts[0]
	case 3:
		hostIP = parts[0]
		published = parts[1]
	default:
		return Port{}, false
	}
	published = strings.TrimSpace(published)
	if !publishedPortRe.MatchString(published) {
		return Port{}, false
	}
	return Port{Service: service, HostIP: strings.TrimSpace(hostIP), Published: published, Protocol: protocol}, true
}

func parseBindSource(node *yaml.Node) (string, bool) {
	node = resolveAlias(node)
	if node == nil {
		return "", false
	}
	switch node.Kind {
	case yaml.ScalarNode:
		value := strings.TrimSpace(node.Value)
		if value == "" || strings.Contains(value, "$") {
			return "", false
		}
		parts := strings.Split(value, ":")
		if len(parts) < 2 {
			return "", false
		}
		return hostBindSource(parts[0])
	case yaml.MappingNode:
		var raw struct {
			Type   string `yaml:"type"`
			Source string `yaml:"source"`
		}
		if err := node.Decode(&raw); err != nil {
			return "", false
		}
		if raw.Type != "" && raw.Type != "bind" {
			return "", false
		}
		return hostBindSource(raw.Source)
	default:
		return "", false
	}
}

func hostBindSource(source string) (string, bool) {
	source = strings.TrimSpace(source)
	if source == "" || strings.Contains(source, "$") {
		return "", false
	}
	if strings.HasPrefix(source, "~") {
		return source, true
	}
	if filepath.IsAbs(source) {
		return filepath.Clean(source), true
	}
	return "", false
}

func resolveAlias(node *yaml.Node) *yaml.Node {
	for i := 0; node != nil && node.Kind == yaml.AliasNode && i < 8; i++ {
		node = node.Alias
	}
	return node
}

// Compare returns the host resources both sides declare. It is deliberately
// conservative: a port conflicts only when the protocol matches and the host
// IPs can overlap; equally named external scopes and identical absolute bind
// sources are shared by construction.
func Compare(a, b Declarations) []Conflict {
	var conflicts []Conflict
	for _, pa := range a.Ports {
		for _, pb := range b.Ports {
			if pa.Published != pb.Published || pa.Protocol != pb.Protocol || !hostIPsOverlap(pa.HostIP, pb.HostIP) {
				continue
			}
			conflicts = append(conflicts, Conflict{
				Kind: "port", Resource: pa.Published + "/" + pa.Protocol,
				Service: pa.Service, Other: pb.Service,
			})
		}
	}
	for _, ba := range a.Binds {
		for _, bb := range b.Binds {
			if ba.Source == bb.Source {
				conflicts = append(conflicts, Conflict{Kind: "bind", Resource: ba.Source, Service: ba.Service, Other: bb.Service})
			}
		}
	}
	for _, va := range a.ExternalVolumes {
		for _, vb := range b.ExternalVolumes {
			if va == vb {
				conflicts = append(conflicts, Conflict{Kind: "external_volume", Resource: va})
			}
		}
	}
	for _, na := range a.ExternalNetworks {
		for _, nb := range b.ExternalNetworks {
			if na == nb {
				conflicts = append(conflicts, Conflict{Kind: "external_network", Resource: na})
			}
		}
	}
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].Kind != conflicts[j].Kind {
			return conflicts[i].Kind < conflicts[j].Kind
		}
		if conflicts[i].Resource != conflicts[j].Resource {
			return conflicts[i].Resource < conflicts[j].Resource
		}
		return conflicts[i].Service < conflicts[j].Service
	})
	return conflicts
}

func hostIPsOverlap(a, b string) bool {
	wildcard := func(ip string) bool {
		switch ip {
		case "", "0.0.0.0", "::", "[::]":
			return true
		}
		return false
	}
	if wildcard(a) || wildcard(b) {
		return true
	}
	return a == b
}
