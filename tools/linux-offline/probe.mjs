import { spawn } from "node:child_process";
import dns from "node:dns/promises";
import net from "node:net";

async function deniedSocket() {
  return await new Promise((resolve) => {
    const socket = net.connect({ host: "1.1.1.1", port: 443 });
    const timer = setTimeout(() => socket.destroy(new Error("network socket did not fail closed")), 2_000);
    socket.once("connect", () => { clearTimeout(timer); socket.destroy(); resolve(false); });
    socket.once("error", () => { clearTimeout(timer); resolve(true); });
  });
}

async function childDeniedSocket() {
  return await new Promise((resolve, reject) => {
    const program = "const n=require('node:net'),d=require('node:dns').promises;const socket=()=>new Promise(r=>{const s=n.connect({host:'1.1.1.1',port:443});setTimeout(()=>{s.destroy();r(false)},2000);s.on('connect',()=>{s.destroy();r(false)});s.on('error',()=>r(true))});const deny=p=>Promise.race([p.then(()=>false,()=>true),new Promise(r=>setTimeout(()=>r(false),2000))]);Promise.all([socket(),deny(d.resolve4('example.com')),deny(fetch('http://1.1.1.1',{signal:AbortSignal.timeout(2000)}))]).then(v=>process.exit(v.every(Boolean)?0:1));";
    const child = spawn(process.execPath, ["-e", program], { shell: false, stdio: "ignore" });
    child.once("error", reject);
    child.once("exit", (code) => resolve(code === 0));
  });
}

async function deniedDns() {
  return await Promise.race([
    dns.resolve4("example.com").then(() => false, () => true),
    new Promise((resolve) => setTimeout(() => resolve(false), 2_000))
  ]);
}

async function deniedHttp() {
  try {
    await fetch("http://1.1.1.1", { signal: AbortSignal.timeout(2_000) });
    return false;
  } catch {
    return true;
  }
}

if (!(await deniedSocket()) || !(await deniedDns()) || !(await deniedHttp()) || !(await childDeniedSocket())) {
  throw new Error("Linux offline namespace permitted parent or child DNS, socket, or HTTP network access");
}
process.stdout.write("linux-offline namespace verified for parent and child DNS, sockets, and HTTP\n");
