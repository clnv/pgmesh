package main

import (
	"github.com/clnv/pgmesh/sqlcplugin"
	sdkcodegen "github.com/sqlc-dev/plugin-sdk-go/codegen"
)

func main() {
	sdkcodegen.Run(sqlcplugin.Generate)
}
