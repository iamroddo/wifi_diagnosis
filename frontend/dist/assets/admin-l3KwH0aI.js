import"./style-yZxMtTAj.js";function i(t){return document.getElementById(t)}async function o(){i("sessions-container").innerHTML='<p style="color:var(--text-muted)">Loading…</p>';try{const t=await fetch("/api/admin/sessions");if(!t.ok)throw new Error(`HTTP ${t.status}`);const d=await t.json();if(d.length===0){i("sessions-container").innerHTML='<p style="color:var(--text-muted)">No sessions recorded yet.</p>';return}i("sessions-container").innerHTML=d.map(c).join("")}catch(t){i("sessions-container").innerHTML=`<p style="color:var(--critical)">Error loading sessions: ${t.message}</p>`}}function c(t){const d=t.created_at?new Date(t.created_at).toLocaleString():"—",n=t.correlation_status??"—",e=`
    <div class="session-section">
      <h4>Client</h4>
      <dl>
        <dt>IP</dt><dd>${t.client_ip??"—"}</dd>
        <dt>MAC</dt><dd>${t.client_mac??"—"}</dd>
        <dt>Name</dt><dd>${t.client_name||t.client_hostname||"—"}</dd>
        <dt>Correlation</dt><dd>${n}</dd>
      </dl>
    </div>`,s=t.ssid?`
    <div class="session-section">
      <h4>Wi-Fi</h4>
      <dl>
        <dt>SSID</dt><dd>${t.ssid}</dd>
        <dt>Band</dt><dd>${t.band??"—"}</dd>
        <dt>Channel</dt><dd>${t.channel??"—"}</dd>
        <dt>UniFi RSSI</dt><dd>${t.rssi!==void 0?t.rssi+" dBm":"—"}</dd>
        <dt>Retries</dt><dd>${t.retries!==void 0?t.retries+"%":"—"}</dd>
      </dl>
    </div>`:"",a=t.lan_latency||t.download||t.upload?`
    <div class="session-section">
      <h4>Performance</h4>
      <dl>
        ${t.lan_latency?.avg_ms!==void 0?`<dt>LAN Avg</dt><dd>${t.lan_latency.avg_ms} ms</dd>`:""}
        ${t.lan_latency?.loss_pct!==void 0?`<dt>Loss</dt><dd>${t.lan_latency.loss_pct}%</dd>`:""}
        ${t.download?.mbps_avg!==void 0?`<dt>Download</dt><dd>${t.download.mbps_avg} Mbps</dd>`:""}
        ${t.upload?.mbps_avg!==void 0?`<dt>Upload</dt><dd>${t.upload.mbps_avg} Mbps</dd>`:""}
      </dl>
    </div>`:"";return`
    <div class="session-card">
      <h3>${t.id}</h3>
      <div class="session-meta">${d} · ${t.client_ip}</div>
      <div class="session-grid">
        ${e}
        ${s}
        ${a}
      </div>
    </div>`}i("btn-refresh").addEventListener("click",o);o();fetch("/api/config").then(t=>t.json()).then(t=>{if(t.title){document.title=`${t.title} — Admin`;const n=document.querySelector("h1");n&&(n.textContent=`${t.title} — Admin`)}const d=document.getElementById("contact-info");if(d&&(t.contact_name||t.contact_email)){const n=t.contact_name||"",e=t.contact_email;d.innerHTML="Need help? Contact "+(e?`<a href="mailto:${e}">${n||e}</a>`:n),d.classList.remove("hidden")}}).catch(()=>{});
