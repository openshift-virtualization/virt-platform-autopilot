/*
Copyright 2026 The Virt Platform Autopilot Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package assets

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"text/template"
	"text/template/parse"
)

// readAssetFuncName is the renderer template function that embeds raw asset
// files into rendered output.
const readAssetFuncName = "readAsset"

// readAssetRoot is the filesystem root that readAsset paths are relative to.
const readAssetRoot = "active"

// templateParseFuncs lists the template functions used by asset templates
// that call readAsset. text/template validates function names at parse time,
// so every function used alongside readAsset must appear here. Derivation
// fails with a "function ... not defined" error otherwise, which is the
// intended fail-fast signal to extend this list.
var templateParseFuncs = template.FuncMap{
	readAssetFuncName: func(string) (string, error) { return "", nil },
	"b64enc":          func(string) string { return "" },
	"gzip":            func(string) string { return "" },
	"dig":             func(...any) any { return nil },
}

// deriveAssetStatuses extracts the supporting-file references of cataloged
// assets. Only assets whose template calls readAsset get an entry; the
// result is sorted by asset name for stable diffs between revisions.
func deriveAssetStatuses(catalog *AssetCatalog, files fs.FS) ([]AssetStatus, error) {
	var statuses []AssetStatus
	for i := range catalog.Assets {
		asset := &catalog.Assets[i]
		refs, err := assetSupportingFiles(asset, files)
		if err != nil {
			return nil, err
		}
		if len(refs) == 0 {
			continue
		}
		statuses = append(statuses, AssetStatus{Name: asset.Name, SupportingFiles: refs})
	}
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Name < statuses[j].Name
	})
	return statuses, nil
}

// assetSupportingFiles returns the files the asset's template references via
// readAsset, as paths relative to the assets root ("active/..."), sorted and
// deduplicated. Templates that do not mention readAsset are not parsed, so
// renderer functions used elsewhere never require stubs in
// templateParseFuncs.
func assetSupportingFiles(asset *AssetMetadata, files fs.FS) ([]string, error) {
	if asset.Path == "" {
		return nil, nil
	}
	content, err := fs.ReadFile(files, asset.Path)
	if err != nil {
		return nil, fmt.Errorf("reading template of asset %q: %w", asset.Name, err)
	}
	if !strings.Contains(string(content), readAssetFuncName) {
		return nil, nil
	}
	refs, err := readAssetRefs(asset.Path, string(content))
	if err != nil {
		return nil, fmt.Errorf("parsing template of asset %q: %w", asset.Name, err)
	}

	supporting := make([]string, 0, len(refs))
	for _, ref := range refs {
		path := readAssetRoot + "/" + ref
		info, statErr := fs.Stat(files, path)
		if statErr != nil || info.IsDir() {
			return nil, fmt.Errorf(
				"asset %q references %q via readAsset, but %q is missing",
				asset.Name, ref, path,
			)
		}
		supporting = append(supporting, path)
	}
	sort.Strings(supporting)
	return supporting, nil
}

// readAssetRefs parses text as a Go template and returns the literal paths
// passed to readAsset, deduplicated in source order. Any readAsset invocation
// whose argument is not a single literal string is an error: references must
// be statically resolvable for feature-status generation.
func readAssetRefs(name, text string) ([]string, error) {
	tmpl, err := template.New(name).Funcs(templateParseFuncs).Parse(text)
	if err != nil {
		return nil, err
	}

	var refs []string
	seen := make(map[string]bool)
	var walk func(parse.Node) error
	walk = func(n parse.Node) error {
		switch node := n.(type) {
		case *parse.ListNode:
			for _, child := range node.Nodes {
				if err := walk(child); err != nil {
					return err
				}
			}
		case *parse.ActionNode:
			return walk(node.Pipe)
		case *parse.IfNode:
			if err := walk(node.Pipe); err != nil {
				return err
			}
			if err := walk(node.List); err != nil {
				return err
			}
			if node.ElseList != nil {
				return walk(node.ElseList)
			}
			return nil
		case *parse.RangeNode:
			if err := walk(node.Pipe); err != nil {
				return err
			}
			if err := walk(node.List); err != nil {
				return err
			}
			if node.ElseList != nil {
				return walk(node.ElseList)
			}
			return nil
		case *parse.WithNode:
			if err := walk(node.Pipe); err != nil {
				return err
			}
			if err := walk(node.List); err != nil {
				return err
			}
			if node.ElseList != nil {
				return walk(node.ElseList)
			}
			return nil
		case *parse.PipeNode:
			for _, cmd := range node.Cmds {
				if err := walk(cmd); err != nil {
					return err
				}
			}
		case *parse.CommandNode:
			if len(node.Args) > 0 {
				if ident, ok := node.Args[0].(*parse.IdentifierNode); ok && ident.Ident == readAssetFuncName {
					if len(node.Args) != 2 {
						return fmt.Errorf("%s must be called with a single literal path in %s", readAssetFuncName, name)
					}
					literal, ok := node.Args[1].(*parse.StringNode)
					if !ok {
						return fmt.Errorf("%s must be called with a literal path in %s", readAssetFuncName, name)
					}
					if !seen[literal.Text] {
						seen[literal.Text] = true
						refs = append(refs, literal.Text)
					}
					return nil
				}
			}
			// Nested calls in parentheses parse as sub-pipelines in the
			// command arguments (e.g. {{ printf "%s" (readAsset "x") }}).
			for _, arg := range node.Args {
				switch arg.(type) {
				case *parse.PipeNode, *parse.CommandNode:
					if err := walk(arg); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if err := walk(tmpl.Tree.Root); err != nil {
		return nil, err
	}
	return refs, nil
}
