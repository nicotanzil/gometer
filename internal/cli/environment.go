package cli

import (
	"flag"
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
