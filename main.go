package main

import (
	"context"
	"http-request/pkg/engram"

	sdk "github.com/bubustack/bubu-sdk-go"
)

func main() {
	if err := sdk.Start(context.Background(), engram.New()); err != nil {
		panic(err)
	}
}
