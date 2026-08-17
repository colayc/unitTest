import assert from "node:assert/strict";
import http, { get as httpGet, request as httpRequest } from "node:http";
import http2, { connect as http2Connect } from "node:http2";
import https, { get as httpsGet, request as httpsRequest } from "node:https";
import net from "node:net";
import test from "node:test";
import { installNativeHttpNetworkGuard } from "./native-network-guard.js";

test("native E2E network guard rejects every standard HTTP(S) entry point", () => {
  const originalNetConnect = net.connect;
  const restore = installNativeHttpNetworkGuard();
  try {
    const expected = /native E2E network guard blocked HTTP\(S\)/;
    assert.equal(httpRequest, http.request);
    assert.equal(httpGet, http.get);
    assert.equal(httpsRequest, https.request);
    assert.equal(httpsGet, https.get);
    assert.equal(http2Connect, http2.connect);
    assert.throws(() => http.request("http://127.0.0.1/"), expected);
    assert.throws(() => http.get("http://127.0.0.1/"), expected);
    assert.throws(() => https.request("https://127.0.0.1/"), expected);
    assert.throws(() => https.get("https://127.0.0.1/"), expected);
    assert.throws(() => http2.connect("https://127.0.0.1/"), expected);
    assert.throws(() => fetch("https://127.0.0.1/"), expected);
    assert.equal(net.connect, originalNetConnect);
  } finally {
    restore();
  }
  assert.notEqual(http.request.name, "blockedHttpRequest");
});
