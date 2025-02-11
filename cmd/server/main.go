package main

import (
	"fmt"
	"github.com/filecoin-project/go-address"
	locli "github.com/filecoin-project/venus-auth/cli"
	"github.com/filecoin-project/venus-auth/core"
	"github.com/urfave/cli/v2"
	"os"
)

func main() {
	address.CurrentNetwork = address.Mainnet
	app := newApp()
	err := app.Run(os.Args)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func newApp() (app *cli.App) {
	app = &cli.App{
		Version:  core.Version,
		Commands: locli.Commands,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "config dir path",
			},
			&cli.StringFlag{
				Name:    "repo",
				EnvVars: []string{"OAUTH_HOME"},
				Hidden:  true,
				Value:   "~/.venus-auth",
			},
		},
	}
	return app

}
