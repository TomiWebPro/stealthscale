const esc = s => String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
function apiBase(){
  // Use current prefix (/web or /admin) for API calls
  const p = location.pathname;
  if(p.startsWith('/admin')) return '/admin/api';
  return '/web/api';
}
function setTab(name){
  document.querySelectorAll('.tabs button').forEach(b=>b.classList.toggle('on', b.dataset.tab===name));
  document.querySelectorAll('.view').forEach(v=>v.classList.toggle('on', v.id==='tab-'+name));
  if(name==='nodes') loadNodes();
  if(name==='users') loadUsers();
  if(name==='keys') loadKeys();
  if(name==='policy') loadPolicy();
  if(name==='derp') loadDERP();
  if(name==='health') loadHealth();
}
function getAPIKey(){ return localStorage.getItem('stscale_apikey')||''; }
function setAPIKey(k){ if(k) localStorage.setItem('stscale_apikey', k); else localStorage.removeItem('stscale_apikey'); }
function ensureAPIKey(){
  if(getAPIKey()) return true;
  const k = prompt('StealthScale API key required (WebUI auth).\nCreate one via: stscale apikeys create\nEnter API key (or cancel to continue unauth):');
  if(k){ setAPIKey(k.trim()); return true; }
  return false;
}
async function fetchJSON(path){
  const headers = {};
  const key = getAPIKey();
  if(key) headers['Authorization'] = 'Bearer '+key;
  const r = await fetch(apiBase()+path, {headers});
  if(r.status===401){
    // Prompt for key on 401 and retry once
    if(ensureAPIKey()){
      const key2 = getAPIKey();
      const h2 = key2? {'Authorization':'Bearer '+key2}: {};
      const r2 = await fetch(apiBase()+path, {headers:h2});
      if(!r2.ok) throw new Error(await r2.text());
      return r2.json();
    }
    throw new Error('401 authentication required — set API key via localStorage stscale_apikey or prompt');
  }
  if(!r.ok) throw new Error(await r.text());
  return r.json();
}
async function promptAPIKey(){ const k=prompt('Enter API key to store in localStorage:'); if(k!==null){ setAPIKey(k.trim()); alert(k.trim()?'API key saved (localStorage)':'API key cleared'); } }
async function loadNodes(){
  try{
    const d = await fetchJSON('/nodes');
    const tbody = document.getElementById('nodesBody');
    const count = document.getElementById('nodes-count');
    count.textContent = (d.nodes||[]).length + ' nodes';
    tbody.innerHTML = (d.nodes||[]).map(n=>`<tr>
      <td class="mono">${esc(n.id)}</td>
      <td>${esc(n.hostname||n.givenName)}</td>
      <td class="mono">${esc(n.userID)}</td>
      <td class="mono">${esc((n.ips||[]).join(', '))}</td>
      <td class="mono">${esc((n.tags||[]).join(', '))}</td>
      <td class="mono" style="max-width:280px;overflow:hidden;text-overflow:ellipsis">vless port ${esc(n.vless?.port)}<br><span class="dim">${esc(n.vless?.uuid||'')}</span></td>
    </tr>`).join('') || '<tr><td colspan="6" class="dim" style="text-align:center;padding:30px">No nodes</td></tr>';
  }catch(e){ document.getElementById('nodesBody').innerHTML='<tr><td colspan="6" class="dim">'+esc(e.message)+'</td></tr>'; }
}
async function loadUsers(){
  try{
    const d = await fetchJSON('/users');
    document.getElementById('usersBody').innerHTML = (d.users||[]).map(u=>`<tr>
      <td class="mono">${esc(u.id)}</td><td>${esc(u.name)}</td><td>${esc(u.email)}</td><td>${esc(u.provider)}</td>
    </tr>`).join('') || '<tr><td colspan="4" class="dim" style="text-align:center;padding:30px">No users</td></tr>';
  }catch(e){ document.getElementById('usersBody').innerHTML='<tr><td colspan="4" class="dim">'+esc(e.message)+'</td></tr>'; }
}
async function loadKeys(){
  try{
    const d = await fetchJSON('/preauthkeys');
    document.getElementById('keysBody').innerHTML = (d.preAuthKeys||[]).map(k=>`<tr>
      <td class="mono">${esc(k.id)}</td><td class="mono" style="max-width:220px;overflow:hidden;text-overflow:ellipsis">${esc(k.key)}</td>
      <td class="mono">${esc(k.userID)}</td><td>${k.reusable?'yes':'no'}</td><td>${k.used?'yes':'no'}</td><td class="mono">${esc(k.expiry)}</td>
    </tr>`).join('') || '<tr><td colspan="6" class="dim" style="text-align:center;padding:30px">No keys</td></tr>';
  }catch(e){ document.getElementById('keysBody').innerHTML='<tr><td colspan="6" class="dim">'+esc(e.message)+'</td></tr>'; }
}
async function loadPolicy(){
  try{
    const d = await fetchJSON('/policy');
    document.getElementById('policyBody').textContent = d.policy || '(no policy configured)';
  }catch(e){ document.getElementById('policyBody').textContent = e.message; }
}
async function loadDERP(){
  try{
    const d = await fetchJSON('/derp');
    document.getElementById('derpBody').textContent = JSON.stringify(d, null, 2);
    const st = document.getElementById('derpStatus');
    const ok = d.stealth_satisfied;
    st.innerHTML = `<span class="pill ${ok?'ok':'err'}">${ok?'stealth satisfied':'stealth NOT satisfied — DERP fail-closed'}</span>
      <span class="pill ${d.shouldIncludeDERP?'ok':'warn'}">shouldIncludeDERP: ${d.shouldIncludeDERP}</span>`;
  }catch(e){ document.getElementById('derpBody').textContent = e.message; }
}
async function loadVLESS(){
  const id = document.getElementById('vlessId').value.trim();
  if(!id) return;
  try{
    const d = await fetchJSON('/vless/'+encodeURIComponent(id));
    document.getElementById('vlessBody').textContent = JSON.stringify(d, null, 2) + "\n\nURI: " + (d.uri||'');
  }catch(e){ document.getElementById('vlessBody').textContent = e.message; }
}
async function loadHealth(){
  try{
    const d = await fetchJSON('/health');
    document.getElementById('healthBody').textContent = JSON.stringify(d, null, 2);
  }catch(e){ document.getElementById('healthBody').textContent = e.message; }
}
setInterval(()=>{ const el=document.getElementById('clock'); if(el) el.textContent=new Date().toLocaleTimeString(); },1000);
const c=document.getElementById('clock'); if(c) c.textContent=new Date().toLocaleTimeString();
loadNodes();
