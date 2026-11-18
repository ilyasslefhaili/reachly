const $ = (s, el = document) => el.querySelector(s);
const results = $("#results");

const LABEL = { valid: "Valid", catch_all: "Catch-all (risky)", invalid: "Invalid", unknown: "Unknown", queued: "Queued", running: "Checking…" };

document.querySelectorAll(".tab").forEach(t => t.addEventListener("click", () => {
  document.querySelectorAll(".tab").forEach(x => x.classList.toggle("active", x === t));
  document.querySelectorAll(".pane").forEach(p => p.classList.toggle("hidden", p.id !== t.dataset.tab));
}));

$("#single").addEventListener("submit", async e => {
  e.preventDefault();
  const fd = new FormData(e.target), btn = $("button", e.target);
  btn.disabled = true;
  try {
    const r = await post("/api/lookup", { name: fd.get("name"), company: fd.get("company") });
    track(r.job_id, fd.get("name"), fd.get("company"));
  } catch (err) { showError(err.message); }
  btn.disabled = false;
});

$("#bulk").addEventListener("submit", async e => {
  e.preventDefault();
  const btn = $("button", e.target);
  btn.disabled = true;
  try {
    const r = await post("/api/lookup/bulk", { lines: new FormData(e.target).get("lines") });
    // reverse so the first line ends up on top
    [...r.jobs].reverse().forEach(j => j.job_id ? track(j.job_id, j.name, j.company) : showError(`${j.name}: ${j.error}`));
  } catch (err) { showError(err.message); }
  btn.disabled = false;
});

async function post(url, body) {
  const res = await fetch(url, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  const data = await res.json();
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

function track(id, name, company) {
  const card = document.createElement("article");
  card.className = "result";
  results.prepend(card);
  render(card, { name, company, state: "queued" });
  const poll = async () => {
    try {
      const j = await (await fetch(`/api/jobs/${id}`)).json();
      render(card, j);
      if (j.state === "queued" || j.state === "running") setTimeout(poll, 800);
    } catch { setTimeout(poll, 2000); }
  };
  poll();
}

function esc(s) { return String(s ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c])); }
function badge(s) { return `<span class="badge ${esc(s)}">${esc(LABEL[s] || s)}</span>`; }

function render(card, j) {
  const head = `<div class="result-head"><span class="who">${esc(j.name)} · ${esc(j.company)}</span>`;
  if (j.state === "queued" || j.state === "running") {
    card.innerHTML = `${head}<span class="meta"><span class="spinner"></span> ${LABEL[j.state]}</span></div>`;
    return;
  }
  if (j.state === "failed") {
    card.innerHTML = `${head}</div><div class="err">${esc(j.error)}</div>`;
    return;
  }
  const r = j.result, b = r.best;
  const meta = `${esc(r.domain)} · ${esc(r.provider)} · ${esc(r.mx_host)} · ${(r.duration_ms / 1000).toFixed(1)}s`;
  const best = b
    ? `<div class="best"><span class="email">${esc(b.email)}</span>${badge(b.status)}<span class="meta">${b.confidence}% confidence</span><button class="copy" data-copy="${esc(b.email)}">Copy</button></div>`
    : `<div class="best"><span class="meta">No address was accepted: every pattern was rejected by the mail server.</span></div>`;
  const hint = r.catch_all === "yes"
    ? `<div class="note">This domain accepts every address (catch-all), so SMTP cannot confirm the mailbox. Showing the most common pattern as a best guess.</div>` : "";
  const note = r.note ? `<div class="note">${esc(r.note)}</div>` : "";
  const rows = r.candidates.map(c => `<tr><td>${esc(c.email)}</td><td>${esc(c.pattern)}</td><td>${badge(c.status)}</td><td>${c.confidence}%</td><td class="msg">${c.smtp_code || ""} ${esc(c.smtp_message)}</td></tr>`).join("");
  card.innerHTML = `${head}<span class="meta">${meta}</span></div>${best}${hint}${note}
    <details><summary>All ${r.candidates.length} patterns checked</summary><div class="table-wrap"><table>
    <thead><tr><th>Email</th><th>Pattern</th><th>Status</th><th>Conf.</th><th>SMTP reply</th></tr></thead><tbody>${rows}</tbody></table></div></details>`;
}

results.addEventListener("click", e => {
  const b = e.target.closest(".copy");
  if (!b) return;
  navigator.clipboard.writeText(b.dataset.copy).then(() => { b.textContent = "Copied"; setTimeout(() => b.textContent = "Copy", 1200); });
});

function showError(msg) {
  const card = document.createElement("article");
  card.className = "result";
  card.innerHTML = `<div class="err">${esc(msg)}</div>`;
  results.prepend(card);
}
