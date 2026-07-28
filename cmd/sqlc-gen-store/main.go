package main

import (
	sdkcodegen "github.com/sqlc-dev/plugin-sdk-go/codegen"
	"github.com/sundayfun/pgmesh/sqlcplugin"
)

func main() {
	sdkcodegen.Run(sqlcplugin.Generate)
}
