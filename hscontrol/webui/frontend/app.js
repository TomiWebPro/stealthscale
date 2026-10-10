const esc = (s) =>
  String(s ?? "").replace(
    /[&<>"']/g,
    (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c],
  );
function apiBase() {
  // Use current prefix (/web or /admin) for API calls
  const p = location.pathname;
  if (p.startsWith("/admin")) return "/admin/api";
  return "/web/api";
}
function setTab(name) {
  document.querySelectorAll(".tabs button").forEach((b) => b.classList.toggle("on", b.dataset.tab === name));
  document.querySelectorAll(".view").forEach((v) => v.classList.toggle("on", v.id === "tab-" + name));
  if (name === "nodes") loadNodes();
  if (name === "users") loadUsers();
  if (name === "keys") loadKeys();
  if (name === "apikeys") loadAPIKeys();
  if (name === "auth") authStatus();
  if (name === "policy") loadPolicy();
  if (name === "derp") loadDERP();
  if (name === "election") loadElection();
  if (name === "health") loadHealth();
}
function getAPIKey() {
  return localStorage.getItem("stscale_apikey") || "";
}
function setAPIKey(k) {
  if (k) localStorage.setItem("stscale_apikey", k);
  else localStorage.removeItem("stscale_apikey");
}
function ensureAPIKey() {
  if (getAPIKey()) return true;
  const k = prompt(
    "StealthScale API key required (WebUI auth).\nCreate one via: stscale apikeys create\nEnter API key (or cancel to continue unauth):",
  );
  if (k) {
    setAPIKey(k.trim());
    return true;
  }
  return false;
}
async function fetchJSON(path) {
  const headers = {};
  const key = getAPIKey();
  if (key) headers["Authorization"] = "Bearer " + key;
  const r = await fetch(apiBase() + path, { headers });
  if (r.status === 401) {
    // Prompt for key on 401 and retry once
    if (ensureAPIKey()) {
      const key2 = getAPIKey();
      const h2 = key2 ? { Authorization: "Bearer " + key2 } : {};
      const r2 = await fetch(apiBase() + path, { headers: h2 });
      if (!r2.ok) throw new Error(await r2.text());
      return r2.json();
    }
    throw new Error("401 authentication required — set API key via localStorage stscale_apikey or prompt");
  }
  if (!r.ok) throw new Error(await r.text());
  return r.json();
}
// fetchJSONMethod sends a state-changing request (POST/DELETE) with
// the stored API key, mirroring fetchJSON's 401-retry behaviour.
async function fetchJSONMethod(path, method, body) {
  const doFetch = (key) => {
    const headers = { "Content-Type": "application/json" };
    if (key) headers["Authorization"] = "Bearer " + key;
    const opts = { method, headers };
    if (body !== undefined) opts.body = JSON.stringify(body);
    return fetch(apiBase() + path, opts);
  };
  let r = await doFetch(getAPIKey());
  if (r.status === 401 && ensureAPIKey()) {
    r = await doFetch(getAPIKey());
  }
  if (!r.ok) throw new Error(await r.text());
  return r.json();
}
async function promptAPIKey() {
  const k = prompt("Enter API key to store in localStorage:");
  if (k !== null) {
    setAPIKey(k.trim());
    alert(k.trim() ? "API key saved (localStorage)" : "API key cleared");
  }
}
async function loadNodes() {
  try {
    const d = await fetchJSON("/nodes");
    const tbody = document.getElementById("nodesBody");
    const count = document.getElementById("nodes-count");
    count.textContent = (d.nodes || []).length + " nodes";
    tbody.innerHTML =
      (d.nodes || [])
        .map(
          (n) => `<tr>
      <td class="mono">${esc(n.id)}</td>
      <td>${esc(n.hostname || n.givenName)}</td>
      <td class="mono">${esc(n.userID)}</td>
      <td class="mono">${esc((n.ips || []).join(", "))}</td>
      <td class="mono">${esc((n.tags || []).join(", "))}</td>
      <td class="mono" style="max-width:280px;overflow:hidden;text-overflow:ellipsis">vless port ${esc(n.vless?.port)}<br><span class="dim">${esc(n.vless?.uuid || "")}</span></td>
      <td class="actions">
        <button onclick="nodeExpire('${esc(n.id)}','${esc(n.hostname || n.givenName)}')">Expire</button>
        <button onclick="nodeRename('${esc(n.id)}','${esc(n.hostname || n.givenName)}')">Rename</button>
        <button onclick="nodeTags('${esc(n.id)}')">Tag</button>
        <button onclick="nodeRoutes('${esc(n.id)}')">Routes</button>
      </td>
    </tr>`,
        )
        .join("") || '<tr><td colspan="7" class="dim" style="text-align:center;padding:30px">No nodes</td></tr>';
  } catch (e) {
    document.getElementById("nodesBody").innerHTML = '<tr><td colspan="7" class="dim">' + esc(e.message) + "</td></tr>";
  }
}
async function loadUsers() {
  try {
    const d = await fetchJSON("/users");
    document.getElementById("usersBody").innerHTML =
      (d.users || [])
        .map(
          (u) => `<tr>
      <td class="mono">${esc(u.id)}</td><td>${esc(u.name)}</td><td>${esc(u.email)}</td><td>${esc(u.provider)}</td>
    </tr>`,
        )
        .join("") || '<tr><td colspan="4" class="dim" style="text-align:center;padding:30px">No users</td></tr>';
  } catch (e) {
    document.getElementById("usersBody").innerHTML = '<tr><td colspan="4" class="dim">' + esc(e.message) + "</td></tr>";
  }
}
async function loadKeys() {
  try {
    const d = await fetchJSON("/preauthkeys");
    document.getElementById("keysBody").innerHTML =
      (d.preAuthKeys || [])
        .map(
          (k) => `<tr>
      <td class="mono">${esc(k.id)}</td><td class="mono" style="max-width:220px;overflow:hidden;text-overflow:ellipsis">${esc(k.key)}</td>
      <td class="mono">${esc(k.userID)}</td><td>${k.reusable ? "yes" : "no"}</td><td>${k.used ? "yes" : "no"}</td><td class="mono">${esc(k.expiry)}</td>
      <td class="actions">
        <button onclick="keyExpire('${esc(k.id)}')">Expire</button>
        <button onclick="keyDelete('${esc(k.id)}')">Delete</button>
      </td>
    </tr>`,
        )
        .join("") || '<tr><td colspan="7" class="dim" style="text-align:center;padding:30px">No keys</td></tr>';
  } catch (e) {
    document.getElementById("keysBody").innerHTML = '<tr><td colspan="7" class="dim">' + esc(e.message) + "</td></tr>";
  }
}
async function loadPolicy() {
  try {
    const d = await fetchJSON("/policy");
    document.getElementById("policyBody").textContent = d.policy || "(no policy configured)";
    const draft = document.getElementById("policyCheckInput");
    if (draft && !draft.value) draft.value = d.policy || "";
  } catch (e) {
    document.getElementById("policyBody").textContent = e.message;
  }
}
async function loadDERP() {
  try {
    const d = await fetchJSON("/derp");
    document.getElementById("derpBody").textContent = JSON.stringify(d, null, 2);
    const st = document.getElementById("derpStatus");
    const ok = d.stealth_satisfied;
    st.innerHTML = `<span class="pill ${ok ? "ok" : "err"}">${ok ? "stealth satisfied" : "stealth NOT satisfied — DERP fail-closed"}</span>
      <span class="pill ${d.shouldIncludeDERP ? "ok" : "warn"}">shouldIncludeDERP: ${d.shouldIncludeDERP}</span>`;
  } catch (e) {
    document.getElementById("derpBody").textContent = e.message;
  }
}
async function loadVLESS() {
  const id = document.getElementById("vlessId").value.trim();
  if (!id) return;
  try {
    const d = await fetchJSON("/vless/" + encodeURIComponent(id));
    document.getElementById("vlessBody").textContent = JSON.stringify(d, null, 2) + "\n\nURI: " + (d.uri || "");
  } catch (e) {
    document.getElementById("vlessBody").textContent = e.message;
  }
}
async function loadElection() {
  try {
    const d = await fetchJSON("/election");
    const rows = [
      ["Mode", d.mode || "(none)"],
      ["Primary node", d.has_primary ? String(d.primary_node) : "(not elected)"],
      ["XRay enabled", d.xray_enabled ? "yes" : "no"],
    ];
    document.getElementById("electionBody").innerHTML = rows
      .map((r) => `<tr><td class="mono">${esc(r[0])}</td><td>${esc(r[1])}</td></tr>`)
      .join("");
    // Show action button for manual mode
    const actions = document.getElementById("electionActions");
    if (d.mode === "manual") {
      actions.innerHTML = `<button onclick="setElectionPrimary()">Set primary (manual)</button>`;
    } else {
      actions.innerHTML = '<span class="dim">Auto-election is active — no manual control needed.</span>';
    }
  } catch (e) {
    document.getElementById("electionBody").innerHTML =
      '<tr><td colspan="2" class="dim">' + esc(e.message) + "</td></tr>";
  }
}
async function setElectionPrimary() {
  const id = prompt("Enter node ID to set as primary coordinator (e.g. 1):");
  if (!id) return;
  try {
    await fetchJSONMethod("/election", "POST", { mode: "manual", node_id: parseInt(id, 10) });
    alert("Primary set successfully.");
    loadElection();
  } catch (e) {
    alert(e.message);
  }
}
async function loadHealth() {
  try {
    const d = await fetchJSON("/health");
    document.getElementById("healthBody").textContent = JSON.stringify(d, null, 2);
  } catch (e) {
    document.getElementById("healthBody").textContent = e.message;
  }
}
async function nodeExpire(id, hostname) {
  const v = prompt(`Expiry for node ${hostname || id} (RFC3339 timestamp, or "never"):`);
  if (v === null || v.trim() === "") return;
  try {
    await fetchJSONMethod("/nodes/" + encodeURIComponent(id) + "/expire", "POST", { expiry: v.trim() });
    alert("Node expiry updated.");
    loadNodes();
  } catch (e) {
    alert(e.message);
  }
}
async function nodeRename(id, hostname) {
  const v = prompt(`New name for node ${hostname || id}:`);
  if (v === null || v.trim() === "") return;
  try {
    await fetchJSONMethod("/nodes/" + encodeURIComponent(id) + "/rename", "POST", { name: v.trim() });
    alert("Node renamed.");
    loadNodes();
  } catch (e) {
    alert(e.message);
  }
}
async function nodeTags(id) {
  const v = prompt(`Tags for node ${id} (comma-separated, e.g. tag:server,tag:db):`);
  if (v === null || v.trim() === "") return;
  const tags = v.split(",").map((t) => t.trim()).filter((t) => t);
  if (tags.length === 0) return;
  try {
    await fetchJSONMethod("/nodes/" + encodeURIComponent(id) + "/tags", "POST", { tags });
    alert("Node tags set.");
    loadNodes();
  } catch (e) {
    alert(e.message);
  }
}
async function nodeRoutes(id) {
  const v = prompt(`Approved routes for node ${id} (comma-separated, e.g. 10.0.0.0/24,192.168.0.0/16):`);
  if (v === null || v.trim() === "") return;
  const routes = v.split(",").map((t) => t.trim()).filter((t) => t);
  if (routes.length === 0) return;
  try {
    await fetchJSONMethod("/nodes/" + encodeURIComponent(id) + "/routes", "POST", { routes });
    alert("Approved routes updated.");
    loadNodes();
  } catch (e) {
    alert(e.message);
  }
}
async function keyExpire(id) {
  if (!confirm(`Expire pre-auth key ${id}?`)) return;
  try {
    await fetchJSONMethod("/preauthkeys/" + encodeURIComponent(id) + "/expire", "POST", {});
    loadKeys();
  } catch (e) {
    alert(e.message);
  }
}
async function keyDelete(id) {
  if (!confirm(`Delete pre-auth key ${id}? This cannot be undone.`)) return;
  try {
    await fetchJSONMethod("/preauthkeys/" + encodeURIComponent(id), "DELETE");
    loadKeys();
  } catch (e) {
    alert(e.message);
  }
}
async function loadAPIKeys() {
  try {
    const d = await fetchJSON("/apikeys");
    document.getElementById("apiKeysBody").innerHTML =
      (d.apiKeys || [])
        .map(
          (k) => `<tr>
      <td class="mono">${esc(k.id)}</td><td class="mono">${esc(k.prefix)}</td><td class="mono">${esc(k.userID ?? "")}</td>
      <td class="mono">${esc(k.createdAt || "")}</td><td class="mono">${esc(k.expiration || "never")}</td>
      <td class="actions">
        <button onclick="apiKeyExpire('${esc(k.id)}')">Expire</button>
        <button onclick="apiKeyDelete('${esc(k.id)}')">Delete</button>
      </td>
    </tr>`,
        )
        .join("") || '<tr><td colspan="6" class="dim" style="text-align:center;padding:30px">No API keys</td></tr>';
  } catch (e) {
    document.getElementById("apiKeysBody").innerHTML = '<tr><td colspan="6" class="dim">' + esc(e.message) + "</td></tr>";
  }
}
async function createAPIKey() {
  const v = prompt("Expiration for the new API key (RFC3339 timestamp, or leave empty for never):", "");
  if (v === null) return;
  try {
    const d = await fetchJSONMethod("/apikeys", "POST", v.trim() ? { expiry: v.trim() } : {});
    alert("API key created (shown once — copy it now):\n\n" + (d.key || "(no key returned)"));
    loadAPIKeys();
  } catch (e) {
    alert(e.message);
  }
}
async function apiKeyExpire(id) {
  if (!confirm(`Expire API key ${id}?`)) return;
  try {
    await fetchJSONMethod("/apikeys/" + encodeURIComponent(id) + "/expire", "POST", {});
    loadAPIKeys();
  } catch (e) {
    alert(e.message);
  }
}
async function apiKeyDelete(id) {
  if (!confirm(`Delete API key ${id}? This cannot be undone.`)) return;
  try {
    await fetchJSONMethod("/apikeys/" + encodeURIComponent(id), "DELETE");
    loadAPIKeys();
  } catch (e) {
    alert(e.message);
  }
}
async function authStatus() {
  const id = document.getElementById("authId").value.trim();
  if (!id) return;
  try {
    const d = await fetchJSON("/auth/" + encodeURIComponent(id));
    document.getElementById("authBody").textContent = JSON.stringify(d, null, 2);
  } catch (e) {
    document.getElementById("authBody").textContent = e.message;
  }
}
async function authDecide(approve) {
  const id = document.getElementById("authId").value.trim();
  if (!id) return;
  try {
    await fetchJSONMethod("/auth/" + encodeURIComponent(id) + (approve ? "/approve" : "/reject"), "POST", {});
    alert("Auth request " + (approve ? "approved" : "rejected") + ".");
    authStatus();
  } catch (e) {
    alert(e.message);
  }
}
async function authApprove() {
  authDecide(true);
}
async function authReject() {
  authDecide(false);
}
async function checkPolicy() {
  const pol = document.getElementById("policyCheckInput").value;
  const result = document.getElementById("policyCheckResult");
  if (!pol.trim()) {
    result.textContent = "Enter a policy to validate.";
    return;
  }
  try {
    await fetchJSONMethod("/policy/check", "POST", { policy: pol });
    result.textContent = "OK — policy is valid against the live users and nodes.";
  } catch (e) {
    result.textContent = "Invalid policy: " + e.message;
  }
}
setInterval(() => {
  const el = document.getElementById("clock");
  if (el) el.textContent = new Date().toLocaleTimeString();
}, 1000);
const c = document.getElementById("clock");
if (c) c.textContent = new Date().toLocaleTimeString();
loadNodes();
