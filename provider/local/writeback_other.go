//go:build !unix

package provider

import "os"

func preserveLocalOwner(_ *os.File, _ os.FileInfo) error { return nil }
