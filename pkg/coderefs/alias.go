package coderefs

import (
	"github.com/launchdarkly/ld-find-code-refs/internal/helpers"
	"github.com/launchdarkly/ld-find-code-refs/internal/options"
)

func generateAliases(flags []string, aliases []options.Alias) (map[string][]string, error) {
	ret := make(map[string][]string, len(flags))
	for _, flag := range flags {
		for _, a := range aliases {
			flagAliases, err := a.Generate(flag)
			if err != nil {
				return nil, err
			}
			ret[flag] = append(ret[flag], flagAliases...)
		}
		ret[flag] = helpers.Dedupe(ret[flag])
	}
	return ret, nil
}
