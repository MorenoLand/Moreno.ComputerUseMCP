package platform

import "github.com/MorenoLand/Moreno.ComputerUseMCP/internal/computer"

func New() computer.Backend {
	return newBackend()
}
