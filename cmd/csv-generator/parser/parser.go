/*
Copyright 2026 The KubeVirt Authors.

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

package parser

import (
	"fmt"
	"os"
	"strings"
)

// EnvVar represents an environment variable name-value pair.
type EnvVar struct {
	Name  string
	Value string
}

// ParseAdditionalImages parses a comma-separated list of ENVKEY:image pairs
// and returns a slice of EnvVar structs.
func ParseAdditionalImages(images string) []EnvVar {
	if images == "" {
		return nil
	}

	var envVars []EnvVar
	pairs := strings.Split(images, ",")
	for _, pair := range pairs {
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 {
			fmt.Fprintf(os.Stderr, "warning: ignoring malformed image pair %q (expected ENVKEY:image)\n", pair)
			continue
		}
		envKey := strings.TrimSpace(parts[0])
		image := strings.TrimSpace(parts[1])
		if envKey == "" || image == "" {
			fmt.Fprintf(os.Stderr, "warning: ignoring empty key or image in pair %q\n", pair)
			continue
		}
		envVars = append(envVars, EnvVar{
			Name:  envKey,
			Value: image,
		})
	}
	return envVars
}
