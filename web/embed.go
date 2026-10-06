package web

import _ "embed"

//go:embed client.js
var Client []byte

//go:embed client.css
var ClientCSS []byte

//go:embed admin.html
var Admin []byte

//go:embed demo.html
var Demo []byte

//go:embed howto.html
var Howto []byte

//go:embed admin.js
var AdminJS []byte
