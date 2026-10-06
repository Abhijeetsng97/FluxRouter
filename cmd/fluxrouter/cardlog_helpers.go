package main

import (
	"github.com/abhijeet/fluxrouter/internal/cardlog"
)

func cardlogOpen(dataDir string) (*cardlog.CardLog, error) { return cardlog.New(dataDir) }