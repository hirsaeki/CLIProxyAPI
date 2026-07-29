//go:build windows

package main

import "golang.org/x/sys/windows"

func replaceFile(source, destination string) error {
	sourcePath, errSource := windows.UTF16PtrFromString(source)
	if errSource != nil {
		return errSource
	}
	destinationPath, errDestination := windows.UTF16PtrFromString(destination)
	if errDestination != nil {
		return errDestination
	}
	return windows.MoveFileEx(
		sourcePath,
		destinationPath,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}
