package main

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type Colors struct {
	accent     tcell.Color
	foreground tcell.Color
	background tcell.Color
	// title refers to now_playing_title in config file
	title    tcell.Color
	popup    tcell.Color
	playlist tcell.Color
}

func newColor() *Colors {

	defaultColors := map[string]string{
		"Color.accent":            "#008B8B",
		"Color.foreground":        "#FFFFFF",
		"Color.background":        "none",
		"Color.popup":             "#0A0F14",
		"Color.now_playing_title": "#017702",
		"Color.playlist":          "#008B8B",
	}

	anko := gomu.anko

	// Validate hex color
	for k, v := range defaultColors {

		// color from the config file
		cfgColor := anko.GetString(k)
		if validHexColor(cfgColor) {
			continue
		}

		// use default value if invalid hex color was given
		anko.Set(k, v)
	}

	// handle none background color
	var bgColor tcell.Color
	bg := anko.GetString("Color.background")

	if bg == "none" {
		bgColor = tcell.ColorDefault
	} else {
		bgColor = tcell.GetColor(bg)
	}

	accent := anko.GetString("Color.accent")
	foreground := anko.GetString("Color.foreground")
	popup := anko.GetString("Color.popup")
	title := anko.GetString("Color.now_playing_title")
	playlist := anko.GetString("Color.playlist")

	color := &Colors{
		accent:     tcell.GetColor(accent),
		foreground: tcell.GetColor(foreground),
		background: bgColor,
		popup:      tcell.GetColor(popup),
		title:      tcell.GetColor(title),
		playlist:   tcell.GetColor(playlist),
	}
	return color
}

func colorsPopup() tview.Primitive {

	textView := tview.NewTextView().
		SetWrap(true).
		SetDynamicColors(true).
		SetWrap(true).
		SetWordWrap(true)

	textView.
		SetBorder(true).
		SetTitle(" Colors ").
		SetBorderPadding(1, 1, 2, 2)


	for i := tcell.ColorBlack; i <= tcell.ColorYellowGreen; i++ {
		fmt.Fprintf(textView, "%-3d [:#%06x]   [-:-] ", i - tcell.ColorBlack, i.Hex())
	}

	textView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEsc:
			gomu.pages.RemovePage("show-color-popup")
			gomu.popups.pop()
		}
		return event
	})

	return textView
}
