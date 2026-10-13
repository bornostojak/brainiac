package brainiac

import "embed"

// Skills are compiled into the server image, independent of its working directory.
//
//go:embed skills
var Skills embed.FS

//go:embed scripts/install.sh
var Installer string
