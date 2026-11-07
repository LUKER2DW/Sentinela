//go:build tools

package tailnet

// gomobile bind needs golang.org/x/mobile in this module (scripts/build-tailnet-aar.sh).
import _ "golang.org/x/mobile/bind"
