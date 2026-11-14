//go:build !windows

package main

import "errors"

var errWindowsOnly = errors.New("this command needs Windows")

func cmdDisplays([]string) error { return errWindowsOnly }
func cmdIdentify([]string) error { return errWindowsOnly }
func cmdLayout([]string) error   { return errWindowsOnly }
func cmdApply([]string) error    { return errWindowsOnly }
func cmdServe([]string) error    { return errWindowsOnly }
func cmdStart([]string) error    { return errWindowsOnly }
func cmdStop([]string) error     { return errWindowsOnly }

func doubleClicked() bool         { return false }
func cmdInstall([]string) error   { return errWindowsOnly }
func cmdUninstall([]string) error { return errWindowsOnly }
