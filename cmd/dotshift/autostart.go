package main

import (
	"context"
	"flag"
	"fmt"
)

func init() {
	commands["service"] = command("service", nil, func(ctx context.Context, path string, fs *flag.FlagSet) error {
		if fs.NArg() != 1 || (fs.Arg(0) != "install" && fs.Arg(0) != "uninstall") {
			return fmt.Errorf("usage: dotshift service [-config FILE] install|uninstall")
		}
		return configureAutostart(ctx, path, fs.Arg(0) == "install")
	})
}
