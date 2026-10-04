package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/jasonobrien/terraform-provider-sks3200/internal/provider"
)

// Format examples and generate docs/ from schema descriptions and examples/.
//go:generate terraform fmt -recursive ./examples/
//go:generate go tool tfplugindocs generate --provider-name sks3200

// Overridden with -ldflags "-X main.version=..." for releases.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/jasonobrien/sks3200",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
