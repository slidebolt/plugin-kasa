// Package main is the entry point for the Kasa bundle adapter process.
// It calls framework.Run which connects to the core event bus and delegates
// all lifecycle events to the logic.ModuleHandler.
package main

import (
	"github.com/lms-io/module-framework/pkg/framework"
	 "github.com/slidebolt/plugin-kasa/pkg/logic"
)

func main() {
	framework.Run(logic.NewModuleHandler())
}
