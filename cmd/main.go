package main

import (
	"fmt"
	"github.com/slidebolt/plugin-framework"
	 "github.com/slidebolt/plugin-kasa/pkg/bundle"
	"github.com/slidebolt/plugin-sdk"
)

func main() {
	fmt.Println("Starting Kasa Plugin Sidecar...")
	framework.Init()

	b, err := sdk.RegisterBundle("plugin-kasa")
	if err != nil {
		fmt.Printf("Failed to register bundle: %v\n", err)
		return
	}

	p := bundle.NewPlugin()
	if err := p.Init(b); err != nil {
		fmt.Printf("Failed to init plugin: %v\n", err)
		return
	}

	fmt.Println("Kasa Plugin is running.")
	select {}
}