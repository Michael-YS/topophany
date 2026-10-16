//go:build js && wasm && !collector

package main

import (
	"errors"
	"time"

	"github.com/WhitenWhiten/topophany/internal/tui"
)

const previewMode = true

type previewSession struct{ *tui.Demo }

func (*previewSession) Close() {}

func newBrowserSession(input browserInput, now time.Time) (browserSession, error) {
	if input.Mode == "live" {
		return nil, errors.New("this viewer only supports the synthetic preview")
	}
	d, err := tui.NewDemo(input.Language, now)
	if err != nil {
		return nil, err
	}
	return &previewSession{d}, nil
}
