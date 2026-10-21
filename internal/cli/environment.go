package cli

import (
	"flag"
	"fmt"
	"os"
)

// envString keeps explicit flags above environment settings, including empty values.
func envString(fs *flag.FlagSet, name, variable, fallback, usage string) *string {
	value, exists := os.LookupEnv(variable)
	if !exists {
		value = fallback
	}
	return fs.String(name, value, usage+" (env "+variable+")")
}

// applyEnvBool runs after parsing so an explicit flag overrides even invalid env input.
func applyEnvBool(fs *flag.FlagSet, name, variable string) error {
	explicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			explicit = true
		}
	})
	value, exists := os.LookupEnv(variable)
	if explicit || !exists {
		return nil
	}
	if err := fs.Set(name, value); err != nil {
		return fmt.Errorf("%s must be a boolean (true or false)", variable)
	}
	return nil
}
