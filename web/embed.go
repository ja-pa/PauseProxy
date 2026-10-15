package web

import (
	"bytes"
	"embed"
	"encoding/json"
)

//go:embed client.js
var clientSource []byte

// Bundle styles with the client so a delayed/failed stylesheet request cannot
// expose the tracked page or leave the break screen unstyled after reload.
var Client = func() []byte {
	css, err := json.Marshal(string(ClientCSS))
	if err != nil {
		panic(err)
	}
	return bytes.Replace(clientSource, []byte(`/*__PAUSEPROXY_CSS__*/ ""`), css, 1)
}()

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

// MVP is vendored locally; include its license in the distributed binary.
//go:embed mvp.css MVP-LICENSE.txt
var mvpFiles embed.FS

//go:embed app.css
var AppCSS []byte

var MVPCSS = func() []byte {
	css, err := mvpFiles.ReadFile("mvp.css")
	if err != nil {
		panic(err)
	}
	return css
}()
