(() => {
  const state = { batches: [], current: null, filter: "", toastTimer: null };
  const $ = (selector) => document.querySelector(selector);
  const $$ = (selector) => [...document.querySelectorAll(selector)];
  const statusLabels = { Draft: "草稿", Dispatched: "运输中", ReceivedPartial: "部分签收", Received: "待关闭", Closed: "已归档" };
  const statusRank = { Draft: 0, Dispatched: 1, ReceivedPartial: 2, Received: 2, Closed: 3 };

  async function api(path, options = {}) {
    const response = await fetch(path, { headers: { "Content-Type": "application/json", ...(options.headers || {}) }, ...options });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(payload.error?.message || "请求未完成");
    return payload;
  }

  function toast(message, error = false) {
    const node = $("#toast");
    node.textContent = message;
    node.className = `toast show${error ? " error" : ""}`;
    clearTimeout(state.toastTimer);
    state.toastTimer = setTimeout(() => { node.className = "toast"; }, 3600);
  }

  function formatDate(value) {
    if (!value) return "—";
    return new Intl.DateTimeFormat("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }).format(new Date(value));
  }

  function escapeHTML(value) {
    return String(value ?? "").replace(/[&<>'"]/g, (character) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;" }[character]));
  }

  async function loadBatches() {
    try {
      const query = state.filter ? `?status=${encodeURIComponent(state.filter)}&limit=100` : "?limit=100";
      const page = await api(`/v1/batches${query}`);
      state.batches = page.batches || [];
      renderBatchList();
      if (state.current) {
        const stillThere = state.batches.some((batch) => batch.id === state.current.id);
        if (stillThere) await selectBatch(state.current.id, false);
        else clearWorkspace();
      } else if (state.batches[0]) {
        await selectBatch(state.batches[0].id, false);
      } else {
        clearWorkspace();
      }
    } catch (error) { toast(error.message, true); }
  }

  function renderBatchList() {
    const list = $("#batchList");
    $("#batchCount").textContent = `${state.batches.length} 个批次`;
    if (!state.batches.length) { list.innerHTML = '<div class="empty-list">暂无匹配批次</div>'; return; }
    list.innerHTML = state.batches.map((batch) => `<button class="batch-card${state.current?.id === batch.id ? " selected" : ""}" data-id="${escapeHTML(batch.id)}">
      <div class="batch-card-head"><strong>${escapeHTML(batch.origin)} → ${escapeHTML(batch.destination)}</strong><span class="mini-status">${statusLabels[batch.status] || batch.status}</span></div>
      <div class="batch-card-meta"><span>${escapeHTML(batch.routeDate)}</span><span>${batch.receivedBoxes}/${batch.totalBoxes} 箱 · v${batch.version}</span></div>
    </button>`).join("");
    $$(".batch-card").forEach((button) => button.addEventListener("click", () => selectBatch(button.dataset.id)));
  }

  async function selectBatch(id, announce = true) {
    try {
      const payload = await api(`/v1/batches/${encodeURIComponent(id)}`);
      state.current = payload.batch;
      renderBatchList();
      renderWorkspace();
      await loadEvents();
      if (state.current.status === "Closed") await loadReceipt();
      if (announce) toast("已切换批次");
    } catch (error) { toast(error.message, true); }
  }

  function clearWorkspace() {
    state.current = null;
    $("#emptyState").hidden = false;
    $("#workspace").hidden = true;
    renderBatchList();
  }

  function renderWorkspace() {
    const batch = state.current;
    if (!batch) return clearWorkspace();
    $("#emptyState").hidden = true;
    $("#workspace").hidden = false;
    $("#batchRouteDate").textContent = `路线日期 / ${batch.routeDate}`;
    $("#batchTitle").textContent = `${batch.origin} → ${batch.destination}`;
    $("#batchID").textContent = batch.id;
    $("#batchOrigin").textContent = batch.origin;
    $("#batchDestination").textContent = batch.destination;
    $("#batchVersion").textContent = batch.version;
    $("#batchUpdated").textContent = formatDate(batch.updatedAt);
    const status = $("#batchStatus");
    status.textContent = statusLabels[batch.status] || batch.status;
    status.className = `status-badge ${batch.status === "Closed" || batch.status === "Received" ? "closed" : batch.status === "ReceivedPartial" ? "partial" : ""}`;
    $("#boxCount").textContent = `${batch.boxes.length} 箱`;
    $("#boxTable").innerHTML = batch.boxes.map((box, index) => `<div class="box-row">
      <span class="box-number">${String(index + 1).padStart(2, "0")}</span>
      <div class="box-name"><strong>${escapeHTML(box.label || box.id)}</strong><span>${escapeHTML(box.drugName)} · ${box.quantity} 件</span></div>
      <div class="box-reading"><strong>${box.requiredMinCelsius}–${box.requiredMaxCelsius}°C</strong><span>${box.sealCode ? `封签 ${escapeHTML(box.sealCode)}` : "未封签"}</span></div>
      <span class="box-condition ${box.condition || ""}">${box.acceptedAt ? (box.condition === "accepted" ? "正常" : box.condition === "exception" ? "异常" : "拒收") : "待验收"}</span>
    </div>`).join("");
    renderStepper(batch.status);
    renderAction();
  }

  function renderStepper(status) {
    const rank = statusRank[status] ?? 0;
    const steps = $$(".step");
    steps.forEach((step) => {
      const name = step.dataset.step;
      const stepRank = statusRank[name] ?? 0;
      step.classList.toggle("active", (name === "ReceivedPartial" && (status === "ReceivedPartial" || status === "Received")) || name === status);
      step.classList.toggle("complete", stepRank < rank || (name === "ReceivedPartial" && ["Received", "Closed"].includes(status)));
    });
    $$(".step-connector").forEach((connector, index) => connector.classList.toggle("complete", index < rank));
  }

  function renderAction() {
    const batch = state.current;
    const title = $("#actionTitle");
    const body = $("#actionBody");
    if (batch.status === "Draft") {
      title.textContent = "封签并发运";
      $("#actionSignal").style.background = "var(--amber)";
      body.innerHTML = `<p class="action-copy">为每个药箱登记封签码，确认后进入运输状态。</p><form id="dispatchForm" class="form-stack"><div class="seal-list">${batch.boxes.map((box) => `<label class="seal-row"><span class="seal-box"><strong>${escapeHTML(box.label || box.id)}</strong><span>${escapeHTML(box.drugName)}</span></span><input name="${escapeHTML(box.id)}" required placeholder="封签码"></label>`).join("")}</div><div class="form-actions"><button class="button button-primary"><span class="button-icon">→</span>确认发运</button></div></form>`;
      $("#dispatchForm").addEventListener("submit", submitDispatch);
    } else if (batch.status === "Dispatched") {
      title.textContent = "登记运输交接";
      $("#actionSignal").style.background = "var(--amber)";
      body.innerHTML = `<p class="action-copy">记录运输节点、参与方和实时温度。温度范围：${temperatureRange(batch)}。</p><form id="handoffForm" class="form-stack"><div class="form-grid"><label>交出方<input name="fromParty" value="${escapeHTML(batch.handoffs.at(-1)?.toParty || batch.origin)}" required></label><label>接收方<input name="toParty" placeholder="配送员" required></label><label>地点<input name="location" placeholder="冷库 / 站点" required></label><label>温度 (°C)<input name="temperatureCelsius" type="number" step="0.1" min="${batchMin(batch)}" max="${batchMax(batch)}" value="5" required></label><label class="full">备注<input name="notes" placeholder="可选"></label></div><div class="form-actions"><button class="button button-primary"><span class="button-icon">+</span>登记交接</button></div></form>`;
      $("#handoffForm").addEventListener("submit", submitHandoff);
    } else if (batch.status === "ReceivedPartial") {
      title.textContent = "完成药箱验收";
      $("#actionSignal").style.background = "var(--amber)";
      body.innerHTML = receiveFormHTML(batch);
      $("#receiveForm").addEventListener("submit", submitReceive);
    } else if (batch.status === "Received") {
      title.textContent = "生成签收凭据";
      $("#actionSignal").style.background = "var(--mint)";
      body.innerHTML = `<p class="action-copy">所有药箱均已验收。确认收货人后关闭批次，生成不可变凭据。</p><form id="closeForm" class="form-stack"><label>收货人<input name="receiver" placeholder="卫生站收货员" required></label><div class="form-actions"><button class="button button-primary"><span class="button-icon">✓</span>关闭并签收</button></div></form>`;
      $("#closeForm").addEventListener("submit", submitClose);
    } else {
      title.textContent = "批次已归档";
      $("#actionSignal").style.background = "var(--mint)";
      body.innerHTML = '<div class="action-body"><p class="action-copy">流程已完成，凭据与审计时间线可供核验。</p></div>';
    }
  }

  function temperatureRange(batch) { return `${batchMin(batch)}–${batchMax(batch)}°C`; }
  function batchMin(batch) { return Math.max(...batch.boxes.map((box) => box.requiredMinCelsius)); }
  function batchMax(batch) { return Math.min(...batch.boxes.map((box) => box.requiredMaxCelsius)); }
  function receiveFormHTML(batch) {
    const boxes = batch.boxes.filter((box) => !box.acceptedAt);
    return `<p class="action-copy">逐箱记录数量与状态。异常或拒收时请填写说明。</p><form id="receiveForm" class="form-stack"><div class="receive-list">${boxes.map((box) => `<div class="receive-row"><label class="receive-box"><strong>${escapeHTML(box.label || box.id)}</strong><span>${escapeHTML(box.drugName)} · 上限 ${box.quantity} 件</span><input type="hidden" name="boxID" value="${escapeHTML(box.id)}"></label><input name="quantity" type="number" min="1" max="${box.quantity}" value="${box.quantity}" required><select name="condition"><option value="accepted">正常</option><option value="exception">异常</option><option value="rejected">拒收</option></select><input class="exception-note" name="exceptionNote" placeholder="异常说明（可选）"></div>`).join("")}</div><div class="form-actions"><button class="button button-primary"><span class="button-icon">✓</span>提交验收</button></div></form>`;
  }

  async function submitDispatch(event) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const seals = state.current.boxes.map((box) => ({ boxID: box.id, sealCode: form.get(box.id) }));
    await submit(`/v1/batches/${state.current.id}/dispatch`, { expectedVersion: state.current.version, seals }, "已发运，等待运输交接");
  }

  async function submitHandoff(event) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    await submit(`/v1/batches/${state.current.id}/handoffs`, { expectedVersion: state.current.version, fromParty: form.get("fromParty"), toParty: form.get("toParty"), location: form.get("location"), temperatureCelsius: Number(form.get("temperatureCelsius")), unit: "C", notes: form.get("notes"), idempotencyKey: `web-${Date.now()}` }, "交接已登记");
  }

  async function submitReceive(event) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const boxIDs = form.getAll("boxID");
    const quantities = form.getAll("quantity");
    const conditions = form.getAll("condition");
    const notes = form.getAll("exceptionNote");
    const items = boxIDs.map((boxID, index) => ({ boxID, quantity: Number(quantities[index]), condition: conditions[index], exceptionNote: notes[index] }));
    await submit(`/v1/batches/${state.current.id}/receive-batch`, { expectedVersion: state.current.version, items }, "验收结果已保存");
  }

  async function submitClose(event) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    await submit(`/v1/batches/${state.current.id}/close`, { expectedVersion: state.current.version, receiver: form.get("receiver") }, "批次已关闭，凭据已生成");
  }

  async function submit(path, body, message) {
    try { await api(path, { method: "POST", body: JSON.stringify(body) }); await loadBatches(); toast(message); }
    catch (error) { toast(error.message, true); }
  }

  async function loadEvents() {
    if (!state.current) return;
    try {
      const page = await api(`/v1/batches/${encodeURIComponent(state.current.id)}/events?limit=100`);
      $("#timeline").innerHTML = page.events?.length ? page.events.map((event) => `<div class="timeline-item"><div class="timeline-title"><strong>${escapeHTML(event.summary)}</strong><time>${formatDate(event.occurredAt)}</time></div><div class="timeline-summary">${event.data ? Object.entries(event.data).filter(([key]) => key !== "idempotencyKey").map(([key, value]) => `${escapeHTML(timelineKey(key))} ${escapeHTML(value)}`).join(" · ") : ""}</div></div>`).join("") : '<div class="timeline-empty">暂无审计事件</div>';
    } catch (error) { toast(error.message, true); }
  }

  function timelineKey(key) { return ({ fromParty: "交出", toParty: "接收", location: "地点", temperatureCelsius: "温度", unit: "单位", condition: "状态", quantity: "数量", receiver: "收货人" }[key] || key) + ":"; }

  async function loadReceipt() {
    try {
      const payload = await api(`/v1/batches/${encodeURIComponent(state.current.id)}/receipt`);
      const receipt = payload.receipt;
      $("#receiptPanel").hidden = false;
      $("#receiptContent").innerHTML = `<div class="receipt-meta"><div><span>收货人</span><strong>${escapeHTML(receipt.receiver)}</strong></div><div><span>签署时间</span><strong>${formatDate(receipt.signedAt)}</strong></div></div><div class="receipt-hash">${escapeHTML(receipt.receiptHash)}</div>`;
    } catch (error) { $("#receiptPanel").hidden = true; toast(error.message, true); }
  }

  function addBoxRow(values = {}) {
    const row = document.createElement("div");
    row.className = "box-editor-row";
    row.innerHTML = `<label>药箱 ID<input name="boxID" value="${escapeHTML(values.id || `box-${Date.now()}`)}" required></label><label>标签<input name="label" value="${escapeHTML(values.label || "")}" placeholder="A01" required></label><label>药品名称<input name="drugName" value="${escapeHTML(values.drugName || "")}" placeholder="冷藏疫苗" required></label><label>数量<input name="quantity" type="number" min="1" value="${values.quantity || 1}" required></label><label>最低 °C<input name="min" type="number" step="0.1" value="${values.min ?? 2}" required></label><label>最高 °C<input name="max" type="number" step="0.1" value="${values.max ?? 8}" required></label><button type="button" class="remove-box" title="删除药箱" aria-label="删除药箱">×</button>`;
    row.querySelector(".remove-box").addEventListener("click", () => { if ($$(".box-editor-row").length > 1) row.remove(); else toast("至少保留一个药箱", true); });
    $("#boxEditor").appendChild(row);
  }

  function openCreate() {
    const dialog = $("#createDialog");
    $("#createForm").reset();
    $("#boxEditor").innerHTML = "";
    addBoxRow({ id: "box-a", label: "A01", drugName: "冷藏疫苗", quantity: 10 });
    dialog.showModal();
  }

  async function submitCreate(event) {
    if (event.submitter?.value === "cancel") return;
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const rows = $$(".box-editor-row");
    const read = (row, name) => row.querySelector(`[name="${name}"]`).value;
    const boxes = rows.map((row) => ({ id: read(row, "boxID"), label: read(row, "label"), drugName: read(row, "drugName"), quantity: Number(read(row, "quantity")), requiredMinCelsius: Number(read(row, "min")), requiredMaxCelsius: Number(read(row, "max")) }));
    try {
      await api("/v1/batches", { method: "POST", body: JSON.stringify({ routeDate: form.get("routeDate"), origin: form.get("origin"), destination: form.get("destination"), boxes }) });
      $("#createDialog").close();
      state.filter = "";
      $("#statusFilter").value = "";
      await loadBatches();
      toast("批次已创建");
    } catch (error) { toast(error.message, true); }
  }

  $("#openCreate").addEventListener("click", openCreate);
  $("#emptyCreate").addEventListener("click", openCreate);
  $("#addBox").addEventListener("click", () => addBoxRow());
  $("#createForm").addEventListener("submit", submitCreate);
  $("#refreshBatches").addEventListener("click", loadBatches);
  $("#refreshEvents").addEventListener("click", loadEvents);
  $("#statusFilter").addEventListener("change", (event) => { state.filter = event.target.value; state.current = null; loadBatches(); });
  loadBatches();
})();
