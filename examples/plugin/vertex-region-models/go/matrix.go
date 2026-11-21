package main

import (
	"io"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/vertexlocations"
)

type documentedModel = vertexlocations.DocumentedModel
type locationMatrix = vertexlocations.LocationMatrix

func parseLocationMatrix(reader io.Reader) (locationMatrix, error) {
	return vertexlocations.ParseDiscoveryMatrix(reader)
}
