package context

import "errors"

var errUnsafePath = errors.New("refusing symlink or reparse point")
