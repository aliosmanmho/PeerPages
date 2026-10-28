var e = 1e4, t = 3e3, n = "x-peerpages-ws-shim", r = i("/PeerPages/");
function i(e) {
	if (!e || e === "./") return "/";
	let t = e;
	return t.startsWith("/") || (t = `/${t}`), t.endsWith("/") || (t = `${t}/`), t;
}
`${r}`;
function a(e, t = r) {
	if (!e.startsWith(t)) return !1;
	let n = e.slice(t.length);
	return n === "connect" || n === "connect/" || n === "connect/index.html" || n === "sw.js" || n === "favicon.ico" || n === "ws-shim.js" || n.startsWith("assets/");
}
var o = "<head", s = "<body";
function c(e, t) {
	let n = e.toLowerCase(), r = n.indexOf(o);
	if (r >= 0) {
		let e = n.indexOf(">", r);
		return e >= 0 ? {
			offset: e + 1,
			decided: !0
		} : {
			offset: 0,
			decided: !1
		};
	}
	let i = n.indexOf(s);
	return i >= 0 ? {
		offset: i,
		decided: !0
	} : t || e.length >= 8192 ? {
		offset: 0,
		decided: !0
	} : {
		offset: 0,
		decided: !1
	};
}
function l(e) {
	let t = new TextEncoder().encode(`<script src="${e}"><\/script>`), n = /* @__PURE__ */ new Uint8Array(), r = !1, i = new TextDecoder("utf-8", { fatal: !1 }), a = (e, a) => {
		if (r) return;
		let o = i.decode(n, { stream: !a }), s = c(o, a);
		if (!s.decided) return;
		let l = u(n, s.offset, o);
		e.enqueue(n.subarray(0, l)), e.enqueue(t), e.enqueue(n.subarray(l)), n = /* @__PURE__ */ new Uint8Array(), r = !0;
	};
	return new TransformStream({
		transform(e, t) {
			r ? t.enqueue(e) : (n = d(n, e), a(t, !1));
		},
		flush(e) {
			r || (a(e, !0), !r && n.byteLength > 0 && e.enqueue(n));
		}
	});
}
function u(e, t, n) {
	if (t === 0) return 0;
	let r = n.slice(0, t), i = new TextEncoder().encode(r).byteLength;
	return Math.min(i, e.byteLength);
}
function d(e, t) {
	if (e.byteLength === 0) return t;
	let n = new Uint8Array(e.byteLength + t.byteLength);
	return n.set(e, 0), n.set(t, e.byteLength), n;
}
// src/sw.ts
var f = new URL(self.registration.scope).pathname, p = null, m = 0, h = 5e3, g = 0, _ = [];
function v() {
	let e = _;
	_ = [];
	for (let t of e) t();
}
function y(e) {
	return new Promise((t) => {
		let n = () => {
			clearTimeout(r), t();
		}, r = setTimeout(() => {
			_ = _.filter((e) => e !== n), t();
		}, e);
		_.push(n);
	});
}
self.addEventListener("install", () => {
	self.skipWaiting();
}), self.addEventListener("activate", (e) => {
	e.waitUntil(self.clients.claim());
}), self.addEventListener("message", (e) => {
	let t = e.data;
	if (t && typeof t == "object") switch (t.type) {
		case "bridge-tunnel-ready": {
			let t = e.source;
			t && "id" in t && (p = t.id, m = Date.now()), g = 0, v();
			break;
		}
		case "bridge-tunnel-gone":
			p = null, m = 0, g = 0, v();
			break;
		case "bridge-tunnel-reconnecting":
			p = null, m = 0, g = Date.now() + 6e4;
			break;
		case "bridge-ws-open": {
			let n = e.ports[0];
			if (!n) break;
			e.waitUntil(b(t.path, t.protos, n));
			break;
		}
	}
});
async function b(e, t, n) {
	let r = await O();
	if (!r) n.postMessage({
		type: "ws-close",
		code: 1013,
		reason: "no tunnel"
	}), n.close();
	else try {
		r.postMessage({
			type: "bridge-ws-open-forward",
			path: e,
			protos: t
		}, [n]);
	} catch {
		n.postMessage({
			type: "ws-close",
			code: 1011,
			reason: "bridge failed"
		}), n.close();
	}
}
self.addEventListener("fetch", (e) => {
	let t = e.request, n = new URL(t.url);
	n.origin === self.location.origin && n.pathname.startsWith(f) && (a(n.pathname, f) || e.respondWith(x(t, n)));
});
async function x(n, r) {
	let i = await O();
	if (!i && Date.now() < g && (await y(n.mode === "navigate" ? t : e), i = await O()), !i) return n.mode === "navigate" ? Response.redirect(new URL(`${f}connect/`, self.location.origin).toString(), 302) : new Response("peerpages: tunnel is not open\n", {
		status: 503,
		headers: {
			"content-type": "text/plain; charset=utf-8",
			"cache-control": "no-store"
		}
	});
	let a = null;
	if (n.method !== "GET" && n.method !== "HEAD") try {
		a = await C(n);
	} catch (e) {
		return new Response(`peerpages: request body unreadable (${String(e)})\n`, {
			status: 413,
			headers: { "content-type": "text/plain; charset=utf-8" }
		});
	}
	let o = await w(i, n, r, a);
	if (o === S && (n.method === "GET" || n.method === "HEAD")) {
		await y(n.mode === "navigate" ? t : e);
		let i = await O();
		i && (o = await w(i, n, r, a));
	}
	return o === S ? new Response("peerpages: tunnel is not open\n", {
		status: 502,
		headers: {
			"content-type": "text/plain; charset=utf-8",
			"cache-control": "no-store"
		}
	}) : o;
}
var S = Symbol("retry-tunnel");
async function C(e) {
	let t = await e.clone().arrayBuffer();
	if (t.byteLength === 0) return null;
	if (t.byteLength > 16777216) throw Error(`body too large: ${t.byteLength} bytes`);
	return t;
}
function w(e, t, n, r) {
	return new Promise((i, a) => {
		let o = new MessageChannel(), s = !1, c = null, l = !1, u = [], d = new ReadableStream({
			start(e) {
				c = e;
				for (let t of u) e.enqueue(t);
				u.length = 0, l && e.close();
			},
			cancel() {
				o.port1.close();
			}
		}), p = (e) => {
			c ? c.enqueue(e) : u.push(e);
		}, m = () => {
			if (l = !0, c) try {
				c.close();
			} catch {}
			o.port1.close();
		}, h = (e, t = !1) => {
			if (s) l = !0, c && c.error(Error(e)), o.port1.close();
			else {
				if (s = !0, o.port1.close(), t) {
					i(S);
					return;
				}
				i(new Response(`peerpages: ${e}\n`, {
					status: 502,
					headers: {
						"content-type": "text/plain; charset=utf-8",
						"cache-control": "no-store"
					}
				}));
			}
		};
		o.port1.onmessage = (e) => {
			let t = e.data;
			switch (t.type) {
				case "res":
					if (s) return;
					s = !0, i(T(t, d));
					break;
				case "chunk":
					p(new Uint8Array(t.data));
					break;
				case "end":
					m();
					break;
				case "error": h(t.message, t.retryable === !0);
			}
		};
		let g = {};
		t.headers.forEach((e, t) => {
			g[t.toLowerCase()] = e;
		});
		let _ = `/${n.pathname.slice(f.length)}${n.search}`;
		try {
			e.postMessage({
				type: "bridge-req",
				method: t.method,
				path: _,
				headers: g,
				body: r
			}, r ? [o.port2, r] : [o.port2]);
		} catch (e) {
			a(e instanceof Error ? e : Error(String(e)));
		}
	});
}
function T(e, t) {
	let r = new Headers(e.headers), i = r.get(n) === "1";
	if (r.delete(n), !D(e.status)) return new Response(null, {
		status: e.status,
		headers: r
	});
	let a = i && E(r.get("content-type")) ? t.pipeThrough(l(f + "ws-shim.js")) : t;
	return new Response(a, {
		status: e.status,
		headers: r
	});
}
function E(e) {
	if (!e) return !1;
	let t = e.split(";")[0]?.trim().toLowerCase() ?? "";
	return t === "text/html" || t === "application/xhtml+xml";
}
function D(e) {
	return e !== 204 && e !== 205 && e !== 304;
}
async function O() {
	if (p) {
		let e = await self.clients.get(p);
		if (e) {
			if (Date.now() - m < h) return e;
			if (await k(e)) return m = Date.now(), e;
		}
		p = null, m = 0;
	}
	let e = await self.clients.matchAll({
		type: "window",
		includeUncontrolled: !0
	});
	for (let t of e) if (await k(t)) return p = t.id, m = Date.now(), t;
	return null;
}
function k(e) {
	return new Promise((t) => {
		let n = new MessageChannel(), r = setTimeout(() => {
			n.port1.close(), t(!1);
		}, 1e3);
		n.port1.onmessage = (e) => {
			clearTimeout(r), n.port1.close(), t(e.data?.type === "bridge-probe-reply" && e.data.hasTunnel === !0);
		};
		try {
			e.postMessage({ type: "bridge-probe" }, [n.port2]);
		} catch {
			clearTimeout(r), t(!1);
		}
	});
}

//# sourceMappingURL=sw.js.map