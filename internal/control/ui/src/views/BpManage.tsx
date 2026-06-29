import { createSignal, For, Show, type JSX } from "solid-js";
import { useStore } from "../store/store";
import { postJSON, delJSON, ApiError } from "../api/client";
import { ConfirmButton } from "../shell/ConfirmDialog";
import { ActionError } from "../shell/ActionError";
import type {
  PendingChanges,
  StagedBlueprint,
  SourceView,
  ValidationResult,
} from "../api/types";

// Ports internal/control/ui.html renderBpManage (the form-heavy external-blueprints view):
// the pending-changes "restart to apply" banner, the staged-blueprint list with provenance
// badges + custom-delete, the upload editor (namespace/name/YAML + Validate→diagnostics+
// cardinality, then Save), and the remote-sources panel (per-source metadata + Fetch/Delete +
// add-source form). Mutations follow the canonical postJSON/delJSON → .then(refresh)
// .catch(setActionErr) pattern; destructive ones (delete staged blueprint, delete source)
// gate behind <ConfirmButton>.
//
// SECURITY (I24): a SourceView carries NO secret token — only token_env_var, the NAME of the
// env var holding it. This view never renders or accepts a raw token value. All user/server
// content renders as Solid text interpolation (never innerHTML) so blueprint/source strings
// cannot inject markup.
//
// The /control/blueprints/* routes 404 when blueprint admin is not wired → the store records
// errors["pending"|"staged"|"sources"] and leaves those fields undefined. The forms still
// render (they degrade to empty lists), exactly as the legacy view did off bp*||[] fallbacks.

// agoMs renders a relative "Xs/Xm/Xh/Xd ago" for the source last-fetch timestamp (legacy agoMs).
function agoMs(ms: number): string {
  if (!ms) return "";
  const d = Date.now() - ms;
  if (d < 0) return "just now";
  const s = Math.floor(d / 1000);
  if (s < 60) return s + "s ago";
  const m = Math.floor(s / 60);
  if (m < 60) return m + "m ago";
  const h = Math.floor(m / 60);
  if (h < 24) return h + "h ago";
  return Math.floor(h / 24) + "d ago";
}

// provenance badge: builtin / git:<source> / upload. (git ones are managed via sources, not
// deletable here.) Mirrors the legacy prov classification exactly.
function provClass(prov: string): "builtin" | "git" | "upload" {
  if (prov === "builtin") return "builtin";
  if (prov && prov.startsWith("git:")) return "git";
  return "upload";
}
const isDeletable = (prov: string): boolean => provClass(prov) === "upload";

export function BpManage(): JSX.Element {
  const store = useStore();
  const s = () => store.state;

  const pending = (): PendingChanges | undefined => s().pending;
  const staged = (): StagedBlueprint[] => s().staged ?? [];
  const sources = (): SourceView[] => s().sources ?? [];

  // pending-changes diff (drives the restart banner + the per-source "update available" badge).
  const added = (): string[] => pending()?.added ?? [];
  const removed = (): string[] => pending()?.removed ?? [];
  const changed = (): string[] => pending()?.changed ?? [];
  const pendingTotal = (): number => added().length + removed().length + changed().length;
  const changedSet = () => new Set(changed());

  // ── action error (mutation failures surface here, not silently swallowed) ────
  const [actionErr, setActionErr] = createSignal<string>();
  const runMutation = (p: Promise<unknown>) =>
    void p
      .then(() => { setActionErr(undefined); return store.refresh(); })
      .catch((e: unknown) => setActionErr(e instanceof ApiError ? e.message : String(e)));

  // delete a staged custom blueprint by its (namespaced) name — DELETE blueprints/custom?name=…
  const deleteCustom = (name: string) =>
    runMutation(delJSON("blueprints/custom?name=" + encodeURIComponent(name)));
  // fetch / delete a git source by id.
  const fetchSource = (id: string) =>
    runMutation(postJSON("blueprints/sources/fetch?id=" + encodeURIComponent(id), null));
  const deleteSource = (id: string) =>
    runMutation(delJSON("blueprints/sources?id=" + encodeURIComponent(id)));

  const loading = () => s().loading;

  return (
    <section>
      <style>{VIEW_CSS}</style>
      <div class="pane-head">
        <h1>📦 Custom blueprints</h1>
        <span class="sub">upload, validate, and manage external blueprint sources</span>
      </div>
      <p class="pane-lead">
        Upload YAML blueprints directly or pull from remote git sources. Changes take effect after a
        process restart.
      </p>

      <Show
        when={!loading()}
        fallback={
          <div class="b-loading" data-testid="bpm-loading" role="status">
            <span class="b-spinner" />
            Loading blueprints…
          </div>
        }
      >
        {/* action error banner (mutation .catch; cleared on next successful action) */}
        <ActionError
          message={actionErr}
          testid="bpm-action-error"
          onDismiss={() => setActionErr(undefined)}
        />

        {/* ── pending changes "restart to apply" banner ─────────────────────── */}
        <Show when={pendingTotal() > 0}>
          <div class="bpm-pending" data-testid="bpm-pending">
            <div class="bpm-ph">
              {pendingTotal()} change{pendingTotal() === 1 ? "" : "s"} pending — restart to apply
            </div>
            <ul>
              <For each={added()}>{(n) => <li>+ {n} (added)</li>}</For>
              <For each={removed()}>{(n) => <li>− {n} (removed)</li>}</For>
              <For each={changed()}>{(n) => <li>~ {n} (changed)</li>}</For>
            </ul>
          </div>
        </Show>

        {/* ── staged blueprints list ────────────────────────────────────────── */}
        <section class="sec">
          <div class="sec-label">
            Staged blueprints
            <span class="meta">
              {staged().length} blueprint{staged().length === 1 ? "" : "s"}
            </span>
          </div>
          <div class="panel">
            <Show
              when={staged().length > 0}
              fallback={
                <div class="empty" data-testid="bpm-staged-empty">
                  No custom or git-sourced blueprints staged. Upload one below or add a source.
                </div>
              }
            >
              <For each={staged()}>
                {(bp) => {
                  const cls = provClass(bp.provenance || "upload");
                  return (
                    <div class="bpm-staged-row" data-testid={"bpm-staged-" + bp.name}>
                      <span class="bpm-name">{bp.name}</span>
                      <span class={"bpm-prov " + cls}>
                        {cls === "builtin" ? "builtin" : cls === "git" ? bp.provenance : "upload"}
                      </span>
                      <Show when={isDeletable(bp.provenance || "upload")}>
                        <ConfirmButton
                          class="bpm-btn danger bpm-del-staged"
                          label="✕ Delete"
                          confirmLabel="Delete"
                          testid={"bpm-del-staged-" + bp.name}
                          message={'Delete custom blueprint "' + bp.name + '"?'}
                          onConfirm={() => deleteCustom(bp.name)}
                        />
                      </Show>
                    </div>
                  );
                }}
              </For>
            </Show>
          </div>
        </section>

        {/* ── upload editor ─────────────────────────────────────────────────── */}
        <UploadEditor onSaved={() => void store.refresh()} />

        {/* ── remote sources panel ──────────────────────────────────────────── */}
        <section class="sec">
          <div class="sec-label">
            Remote sources
            <span class="meta">
              {sources().length} source{sources().length === 1 ? "" : "s"}
            </span>
          </div>
          <div class="panel">
            <h3>Git / remote sources</h3>
            <p class="gh">
              Registered sources are fetched automatically and staged for the next restart.
            </p>

            <Show
              when={sources().length > 0}
              fallback={
                <div class="empty" data-testid="bpm-sources-empty">
                  No remote sources configured. Add one below.
                </div>
              }
            >
              <For each={sources()}>
                {(src) => {
                  const hasUpdate = () => changedSet().has(src.id);
                  return (
                    <div class="bpm-source-row" data-testid={"bpm-source-" + src.id}>
                      <div class="bpm-source-head">
                        <span class="bpm-source-name">{src.name || src.id}</span>
                        <Show when={hasUpdate()}>
                          <span class="bpm-update-badge">update available</span>
                        </Show>
                        <div class="bpm-source-actions">
                          <button
                            type="button"
                            class="bpm-btn bpm-src-fetch"
                            title="Fetch now from remote"
                            data-testid={"bpm-source-fetch-" + src.id}
                            onClick={() => fetchSource(src.id)}
                          >
                            Fetch now
                          </button>
                          <ConfirmButton
                            class="bpm-btn danger"
                            label="✕ Delete"
                            confirmLabel="Delete"
                            testid={"bpm-source-del-" + src.id}
                            message={'Delete source "' + (src.name || src.id) + '"?'}
                            onConfirm={() => deleteSource(src.id)}
                          />
                        </div>
                      </div>
                      <div class="bpm-source-meta">
                        <Show when={src.url}>
                          <span>url: {src.url}</span>
                        </Show>
                        <Show when={src.ref}>
                          <span>ref: {src.ref}</span>
                        </Show>
                        <Show when={src.subpath}>
                          <span>subpath: {src.subpath}</span>
                        </Show>
                        <Show when={src.namespace}>
                          <span>namespace: {src.namespace}</span>
                        </Show>
                        {/* SECURITY: only the env-var NAME is ever shown — never a secret value. */}
                        <Show when={src.token_env_var}>
                          <span data-testid={"bpm-source-tokenenv-" + src.id}>
                            token env var: {src.token_env_var}
                          </span>
                        </Show>
                        <Show when={src.last_sha}>
                          <span>sha: {src.last_sha.slice(0, 8)}</span>
                        </Show>
                        <Show when={src.last_fetch_ms}>
                          <span>fetched: {agoMs(src.last_fetch_ms)}</span>
                        </Show>
                        <Show when={src.last_err}>
                          <span style="color:var(--err)">error: {src.last_err}</span>
                        </Show>
                      </div>
                    </div>
                  );
                }}
              </For>
            </Show>

            <AddSourceForm onAdded={() => void store.refresh()} />
          </div>
        </section>
      </Show>
    </section>
  );
}

// ════════════════════════════════════════════════════════════════════════════
// Upload editor — namespace + name + YAML; Validate then Save.
// ════════════════════════════════════════════════════════════════════════════
function UploadEditor(props: { onSaved: () => void }): JSX.Element {
  const [namespace, setNamespace] = createSignal("");
  const [name, setName] = createSignal("");
  const [yaml, setYaml] = createSignal("");
  const [fieldErr, setFieldErr] = createSignal("");
  const [validating, setValidating] = createSignal(false);
  const [saving, setSaving] = createSignal(false);
  const [vres, setVres] = createSignal<ValidationResult>();

  const cardStr = (r: ValidationResult): string =>
    r.cardinality != null && r.cardinality >= 0
      ? "~" + r.cardinality + " series" + (r.estimated ? " (estimated)" : "")
      : "";

  // Validate posts {yaml}; the server always returns 200 with a ValidationResult (ok:false on a
  // bad blueprint, with diagnostics) — so we key off result.ok, not the HTTP status.
  const validate = () => {
    const y = yaml().trim();
    if (!y) { setFieldErr("YAML is required."); return; }
    setFieldErr("");
    setValidating(true);
    postJSON<ValidationResult>("blueprints/validate", { yaml: y })
      .then((r) => setVres(r))
      .catch((e: unknown) =>
        setFieldErr(e instanceof ApiError ? e.message : String(e)),
      )
      .finally(() => setValidating(false));
  };

  // Save posts {namespace, name, yaml}; validates the name client-side first (matches legacy:
  // required, no "/" or "__"). On success clears the form + the validation result and refreshes.
  const save = () => {
    const y = yaml().trim();
    const n = name().trim();
    const ns = namespace().trim();
    if (!y) { setFieldErr("YAML is required."); return; }
    if (!n) { setFieldErr("Blueprint name is required."); return; }
    if (n.includes("/") || n.includes("__")) {
      setFieldErr('Name must not contain "/" or "__".');
      return;
    }
    setFieldErr("");
    setSaving(true);
    postJSON("blueprints/custom", { namespace: ns || undefined, name: n, yaml: y })
      .then(() => {
        setNamespace("");
        setName("");
        setYaml("");
        setVres(undefined);
        props.onSaved();
      })
      .catch((e: unknown) =>
        setFieldErr(e instanceof ApiError ? e.message : String(e)),
      )
      .finally(() => setSaving(false));
  };

  return (
    <section class="sec">
      <div class="sec-label">Upload custom blueprint</div>
      <div class="panel">
        <h3>YAML editor</h3>
        <p class="gh">Paste or type a blueprint YAML. Validate before saving.</p>

        <div class="bpm-form">
          <div class="bpm-row2">
            <div>
              <label>Namespace (optional)</label>
              <input
                type="text"
                placeholder="e.g. team-a (optional)"
                value={namespace()}
                data-testid="bpm-ns"
                onInput={(e) => setNamespace(e.currentTarget.value)}
              />
            </div>
            <div>
              <label>Blueprint name</label>
              <input
                type="text"
                placeholder="e.g. myapp (required; no / or __ in name)"
                value={name()}
                data-testid="bpm-name"
                onInput={(e) => setName(e.currentTarget.value)}
              />
            </div>
          </div>
          <div>
            <label>Blueprint YAML</label>
            <textarea
              placeholder={"# blueprint YAML\nname: myapp\nenvironments:\n  - name: prod\n    …"}
              value={yaml()}
              data-testid="bpm-yaml"
              onInput={(e) => setYaml(e.currentTarget.value)}
            />
          </div>

          <div class="bpm-caveat">
            Validation checks this blueprint alone. Cross-blueprint name collisions (cluster/db/host/
            workload reused across blueprints) are reported in Diagnostics after restart.
          </div>

          <div class="bpm-actions">
            <button
              type="button"
              class="bpm-btn"
              disabled={validating()}
              data-testid="bpm-validate"
              onClick={validate}
            >
              {validating() ? "Validating…" : "Validate"}
            </button>
            <button
              type="button"
              class="bpm-btn primary"
              disabled={saving()}
              data-testid="bpm-save"
              onClick={save}
            >
              {saving() ? "Saving…" : "Save"}
            </button>
          </div>

          <Show when={vres()}>
            {(r) => (
              <div
                class={"bpm-vresult " + (r().ok ? "ok" : "err")}
                data-testid="bpm-vresult"
              >
                <div class="bpm-vsummary">
                  {r().ok
                    ? ["✓ Valid blueprint: " + (r().name || ""), cardStr(r())]
                        .filter(Boolean)
                        .join(" · ")
                    : "✕ Validation failed"}
                </div>
                <Show when={(r().diagnostics ?? []).length > 0}>
                  <div class="bpm-vdiags">
                    <For each={r().diagnostics}>
                      {(d) => <div class="bpm-vdiag">{d}</div>}
                    </For>
                  </div>
                </Show>
              </div>
            )}
          </Show>

          <Show when={fieldErr()}>
            <div class="field-err" data-testid="bpm-field-err">{fieldErr()}</div>
          </Show>
        </div>
      </div>
    </section>
  );
}

// ════════════════════════════════════════════════════════════════════════════
// Add-source form — name + url (required) + optional ref/subpath/namespace/token_env_var.
// SECURITY: token_env_var is the env-var NAME only; there is NO secret-token input here.
// ════════════════════════════════════════════════════════════════════════════
function AddSourceForm(props: { onAdded: () => void }): JSX.Element {
  const [name, setName] = createSignal("");
  const [url, setUrl] = createSignal("");
  const [ref, setRef] = createSignal("");
  const [subpath, setSubpath] = createSignal("");
  const [namespace, setNamespace] = createSignal("");
  const [tokenEnv, setTokenEnv] = createSignal("");
  const [err, setErr] = createSignal("");
  const [saving, setSaving] = createSignal(false);

  const submit = () => {
    const n = name().trim();
    const u = url().trim();
    if (!n) { setErr("Source name is required."); return; }
    if (!u) { setErr("URL is required."); return; }
    setErr("");
    // Build the SourceView POST body; only include optional fields when set (matches legacy).
    const body: Record<string, string> = { name: n, url: u };
    if (ref().trim()) body.ref = ref().trim();
    if (subpath().trim()) body.subpath = subpath().trim();
    if (namespace().trim()) body.namespace = namespace().trim();
    if (tokenEnv().trim()) body.token_env_var = tokenEnv().trim();
    setSaving(true);
    postJSON("blueprints/sources", body)
      .then(() => {
        setName("");
        setUrl("");
        setRef("");
        setSubpath("");
        setNamespace("");
        setTokenEnv("");
        props.onAdded();
      })
      .catch((e: unknown) => setErr(e instanceof ApiError ? e.message : String(e)))
      .finally(() => setSaving(false));
  };

  return (
    <>
      <div class="bpm-addlabel">Add source</div>
      <div class="bpm-form">
        <div class="bpm-row2">
          <div>
            <label>Source name</label>
            <input
              type="text"
              placeholder="e.g. my-org-blueprints (required)"
              value={name()}
              data-testid="bpm-src-name"
              onInput={(e) => setName(e.currentTarget.value)}
            />
          </div>
          <div>
            <label>URL</label>
            <input
              type="text"
              placeholder="https://github.com/…/blueprints.git (required)"
              value={url()}
              data-testid="bpm-src-url"
              onInput={(e) => setUrl(e.currentTarget.value)}
            />
          </div>
        </div>
        <div class="bpm-row2">
          <div>
            <label>Ref (branch/tag)</label>
            <input
              type="text"
              placeholder="main"
              value={ref()}
              data-testid="bpm-src-ref"
              onInput={(e) => setRef(e.currentTarget.value)}
            />
          </div>
          <div>
            <label>Subpath</label>
            <input
              type="text"
              placeholder="blueprints/ (optional path within repo)"
              value={subpath()}
              data-testid="bpm-src-subpath"
              onInput={(e) => setSubpath(e.currentTarget.value)}
            />
          </div>
        </div>
        <div class="bpm-row2">
          <div>
            <label>Namespace prefix (optional)</label>
            <input
              type="text"
              placeholder="team-a (optional namespace prefix)"
              value={namespace()}
              data-testid="bpm-src-namespace"
              onInput={(e) => setNamespace(e.currentTarget.value)}
            />
          </div>
          <div>
            {/* SECURITY: env-var NAME only — never the token value. */}
            <label>Token env var name (NOT the token)</label>
            <input
              type="text"
              placeholder="MY_GITHUB_TOKEN (env var NAME, not the token value)"
              value={tokenEnv()}
              data-testid="bpm-src-tokenenv"
              onInput={(e) => setTokenEnv(e.currentTarget.value)}
            />
          </div>
        </div>
        <div class="bpm-actions">
          <button
            type="button"
            class="bpm-btn primary"
            disabled={saving()}
            data-testid="bpm-src-add"
            onClick={submit}
          >
            {saving() ? "Saving…" : "Add source"}
          </button>
        </div>
        <Show when={err()}>
          <div class="field-err" data-testid="bpm-src-err">{err()}</div>
        </Show>
      </div>
    </>
  );
}

// View-scoped styles, ported from the legacy ui.html <style> block for the bp-manage classes
// (.bpm-pending / .bpm-staged-row / .bpm-prov / .bpm-form / .bpm-btn / .bpm-vresult /
// .bpm-source-row …) plus the shared loading / error / sec / panel chrome the other views use.
const VIEW_CSS = `
.pane-lead { font-size:13px; color:var(--dim); margin:0 0 20px; line-height:1.5; }

.b-loading { display:flex; align-items:center; gap:10px; color:var(--dim); font-size:13px;
  padding:18px; border:1px dashed var(--bd2); border-radius:12px; background:var(--panel2); }
.b-spinner { width:14px; height:14px; border-radius:50%; border:2px solid var(--bd2);
  border-top-color:var(--acc); animation:b-spin .7s linear infinite; flex:none; }
@keyframes b-spin { to { transform:rotate(360deg); } }
.sec { margin-bottom:26px; }
.sec-label { display:flex; align-items:baseline; gap:8px; font:700 11px system-ui; letter-spacing:.8px;
  text-transform:uppercase; color:var(--dim); margin:0 0 10px; }
.sec-label .meta { font-weight:500; text-transform:none; letter-spacing:0; font-size:11.5px; }

.panel { background:var(--panel2); border:1px solid var(--bd); border-radius:12px; padding:16px 18px; }
.panel h3 { font:700 14px system-ui; color:var(--tx); margin:0 0 6px; }
.panel .gh { font-size:12.5px; color:var(--dim); margin:0 0 14px; line-height:1.5; }

.empty { color:var(--dim); font-size:13px; padding:14px; text-align:center;
  border:1px dashed var(--bd2); border-radius:10px; background:var(--panel); }
.field-err { color:var(--err); font-size:11.5px; margin:6px 0; min-height:0; }

.bpm-pending { background:var(--warnbg); border:1px solid var(--warnbd); color:var(--warn);
  border-radius:10px; padding:12px 16px; margin-bottom:18px; font-size:13px; }
.bpm-pending .bpm-ph { font:800 13.5px system-ui; margin-bottom:6px; }
.bpm-pending ul { margin:4px 0 0 16px; padding:0; font-size:12.5px; }

.bpm-staged-row { display:flex; align-items:center; gap:10px; padding:8px 11px;
  border-bottom:1px solid var(--panel); font:12.5px system-ui; }
.bpm-staged-row:last-child { border-bottom:none; }
.bpm-staged-row .bpm-name { font-family:var(--mono); font-weight:600; flex:1; overflow:hidden; text-overflow:ellipsis; }
.bpm-staged-row .bpm-del-staged { font-size:12px; padding:4px 11px; margin-left:auto; }
.bpm-prov { font:700 9.5px system-ui; letter-spacing:.4px; text-transform:uppercase;
  padding:1px 7px; border-radius:6px; flex:none; }
.bpm-prov.builtin { background:var(--acc2); color:var(--acc); border:1px solid var(--accbd); }
.bpm-prov.upload  { background:var(--okbg); color:var(--ok); border:1px solid var(--okbd); }
.bpm-prov.git     { background:var(--panel); color:var(--dim); border:1px solid var(--bd); }

.bpm-form { display:flex; flex-direction:column; gap:11px; }
.bpm-form label { font:600 11px system-ui; color:var(--dim); display:block; margin-bottom:4px; }
.bpm-form input[type=text] { width:100%; font:13px var(--mono); border:1px solid var(--bd);
  border-radius:8px; padding:7px 10px; background:var(--panel); color:var(--tx); }
.bpm-form input[type=text]:focus { outline:none; border-color:var(--acc); }
.bpm-form textarea { width:100%; font:12px var(--mono); border:1px solid var(--bd);
  border-radius:8px; padding:9px 11px; background:var(--panel); color:var(--tx);
  resize:vertical; min-height:220px; box-sizing:border-box; }
.bpm-form textarea:focus { outline:none; border-color:var(--acc); }
.bpm-row2 { display:grid; grid-template-columns:1fr 1fr; gap:12px; }
@media (max-width:700px) { .bpm-row2 { grid-template-columns:1fr; } }

.bpm-actions { display:flex; gap:9px; align-items:center; flex-wrap:wrap; }
.bpm-btn { font:600 13px system-ui; padding:8px 18px; border-radius:9px; cursor:pointer;
  border:1px solid var(--bd); background:var(--panel); color:var(--tx); }
.bpm-btn:hover { border-color:var(--acc); }
.bpm-btn:disabled { opacity:.5; cursor:default; }
.bpm-btn.primary { background:var(--acc); color:#fff; border-color:var(--acc); }
.bpm-btn.primary:hover { filter:brightness(1.06); }
.bpm-btn.danger { color:var(--err); }
.bpm-btn.danger:hover { border-color:var(--err); }

.bpm-vresult { border:1px solid var(--bd); border-radius:9px; padding:11px 13px; margin-top:8px;
  background:var(--panel); font:12.5px system-ui; }
.bpm-vresult.ok  { border-color:var(--okbd); background:var(--okbg); }
.bpm-vresult.err { border-color:var(--critbd); background:var(--crit); }
.bpm-vresult .bpm-vsummary { font-weight:700; margin-bottom:5px; }
.bpm-vresult .bpm-vdiags { font:11.5px var(--mono); margin-top:7px; display:flex; flex-direction:column; gap:4px; }
.bpm-vresult .bpm-vdiag { display:flex; gap:8px; }

.bpm-caveat { font:12px system-ui; color:var(--dim); padding:9px 12px;
  background:var(--panel); border:1px solid var(--bd); border-radius:8px; margin-top:6px; }

.bpm-source-row { display:flex; flex-direction:column; gap:4px; padding:11px 13px;
  border:1px solid var(--bd); border-radius:10px; background:var(--panel); margin-bottom:10px; }
.bpm-source-head { display:flex; align-items:center; gap:10px; }
.bpm-source-name { font:700 13px var(--mono); flex:1; overflow:hidden; text-overflow:ellipsis; }
.bpm-source-actions { margin-left:auto; display:flex; gap:7px; }
.bpm-source-actions .bpm-btn { font-size:12px; padding:4px 11px; }
.bpm-source-meta { font:11px system-ui; color:var(--dim); display:flex; flex-wrap:wrap; gap:8px; margin-top:4px; }
.bpm-source-meta span { font-family:var(--mono); }
.bpm-update-badge { font:700 9px system-ui; letter-spacing:.4px; text-transform:uppercase;
  padding:1px 6px; border-radius:6px; background:var(--warnbg); color:var(--warn); border:1px solid var(--warnbd); }

.bpm-addlabel { margin-top:16px; font:700 11px system-ui; letter-spacing:.6px;
  text-transform:uppercase; color:var(--dim); margin-bottom:8px; }
`;
