package main

import (
	"fmt"
	"testing"

	"github.com/bubustack/bubu-sdk-go/conformance"
	"github.com/bubustack/http-request-engram/pkg/config"
	"github.com/bubustack/http-request-engram/pkg/engram"
)

func TestConformance(t *testing.T) {
	suite := conformance.BatchSuite[config.Config, any]{
		Engram:      engram.New(),
		Config:      config.Config{},
		Inputs:      map[string]any{},
		ExpectError: true,
		ValidateError: func(err error) error {
			if err == nil {
				return nil
			}
			if err.Error() != "input 'url' is a required string" {
				return fmt.Errorf("unexpected conformance error: %w", err)
			}
			return nil
		},
	}
	suite.Run(t)
}
