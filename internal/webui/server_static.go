package webui

import (
	"bytes"
	"io/fs"
	"net/http"
)

// --- Static SPA ---

// authScript is injected into the SPA HTML to extract the token from the URL hash
// and attach it to all API requests (fetch + WebSocket).
const authScript = `<script>
(function(){
	var m=location.hash.match(/token=([a-f0-9]+)/);
	if(!m)return;
	var t=m[1];
	history.replaceState(null,'',location.pathname);
	var origFetch=window.fetch;
	window.fetch=function(url,opts){
		opts=opts||{};
		opts.headers=opts.headers||{};
		if(typeof opts.headers.set==='function'){opts.headers.set('Authorization','Bearer '+t)}
		else{opts.headers['Authorization']='Bearer '+t}
		return origFetch(url,opts);
	};
	var OrigWS=window.WebSocket;
	window.WebSocket=function(url,protocols){
		if(url.indexOf('?')===-1)url+='?token='+t;else url+='&token='+t;
		if(protocols)return new OrigWS(url,protocols);
		return new OrigWS(url);
	};
	window.WebSocket.prototype=OrigWS.prototype;
})();
</script>`

func (s *Server) serveSPA(w http.ResponseWriter, r *http.Request) {
	// All non-API routes serve the SPA index.html
	data, err := fs.ReadFile(spafs, "index.html")
	if err != nil {
		// Fallback: try reading from the raw embed FS
		data, err = fs.ReadFile(spaFS, "dist/index.html")
		if err != nil {
			http.Error(w, "SPA not found: "+err.Error(), http.StatusNotFound)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	// Inject auth script into <head>
	if idx := bytes.Index(data, []byte("<head>")); idx != -1 {
		var buf bytes.Buffer
		buf.Write(data[:idx+6])
		buf.WriteString(authScript)
		buf.Write(data[idx+6:])
		data = buf.Bytes()
	}
	_, _ = w.Write(data)
}
