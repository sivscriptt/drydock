(() => {
  'use strict'

  const M = JSON.parse(document.getElementById('data').textContent)
  const NS = 'http://www.w3.org/2000/svg'
  const $ = (id) => document.getElementById(id)
  const reduced = matchMedia('(prefers-reduced-motion: reduce)').matches
  const svg = $('canvas')
  const viewport = $('viewport')

  function el(tag, attrs, parent, text) {
    const e = document.createElementNS(NS, tag)
    for (const k in attrs) e.setAttribute(k, attrs[k])
    if (text != null) e.textContent = text
    if (parent) parent.appendChild(e)
    return e
  }

  const KIND = {
    start: 'Start', end: 'End', task: 'Task', lookup: 'Lookup', write: 'Save', expression: 'Calculate',
    loop: 'Loop', message: 'Message', document: 'Document', payment: 'Payment', route: 'Route', other: 'Step',
  }
  const W = 236, H = 90
  // One small line glyph per kind, drawn in the card header.
  const GLYPH = {
    start: 'M-2.5-3.5L3.5 0L-2.5 3.5z',
    end: 'M-3-3h6v6h-6z',
    task: 'M0-1.2a2 2 0 1 0 0-4a2 2 0 1 0 0 4zM-3.8 4.2c0-2.3 1.7-3.7 3.8-3.7s3.8 1.4 3.8 3.7',
    lookup: 'M-1-1m-3 0a3 3 0 1 0 6 0a3 3 0 1 0-6 0M1.3 1.3L4 4',
    write: 'M-4-3.5h8v7h-8zM-4-0.5h8',
    expression: 'M3-4h-6l3.4 4l-3.4 4h6',
    loop: 'M3.6-0.8a3.6 3.6 0 1 0-0.4 2.4M3.6-4v3.2h-3.2',
    message: 'M-4.5-3h9v6h-9zM-4.5-3l4.5 3.4l4.5-3.4',
    document: 'M-3-4.5h4l2.6 2.6v6.4h-6.6zM1-4.5v2.6h2.6',
    payment: 'M-4.5-3h9v6h-9zM-4.5-1h9',
    route: 'M0-4l4 4l-4 4l-4-4z',
    other: 'M-3 0h6',
  }

  // ---------------------------------------------------------------- layout
  const g = new dagre.graphlib.Graph({ multigraph: true })
  g.setGraph({ rankdir: 'TB', nodesep: 40, ranksep: 60, edgesep: 16, marginx: 72, marginy: 60 })
  g.setDefaultEdgeLabel(() => ({}))
  const byId = {}
  M.nodes.forEach((n) => { byId[n.id] = n; g.setNode(n.id, { width: W, height: H }) })
  M.edges.forEach((e, i) => {
    if (!byId[e.from] || !byId[e.to]) return
    const lw = e.label ? Math.min(e.label.length * 6.1 + 18, 280) : 0
    g.setEdge(e.from, e.to, { width: lw, height: e.label ? 20 : 0, labelpos: 'c' }, 'e' + i)
  })
  dagre.layout(g)

  // ---------------------------------------------------------------- nodes
  // Rank order (by vertical position) drives the reveal stagger.
  const ys = [...new Set(M.nodes.map((n) => Math.round(g.node(n.id).y)))].sort((a, b) => a - b)
  const rankOf = (id) => ys.indexOf(Math.round(g.node(id).y))
  const delay = (id) => Math.min(rankOf(id) * 45, 1400)

  function wrap(text, max) {
    const words = text.split(/\s+/)
    const lines = ['']
    for (const w of words) {
      const cur = lines[lines.length - 1]
      if ((cur + ' ' + w).trim().length <= max) lines[lines.length - 1] = (cur + ' ' + w).trim()
      else if (lines.length < 2) lines.push(w)
      else { lines[1] = (lines[1] + ' ' + w).slice(0, max - 1) + '…'; break }
    }
    return lines.map((l) => (l.length > max ? l.slice(0, max - 1) + '…' : l))
  }

  const nodeEl = {}
  M.nodes.forEach((n) => {
    const p = g.node(n.id)
    const outer = el('g', { transform: `translate(${p.x - W / 2},${p.y - H / 2})` }, $('nodes'))
    const gn = el('g', { class: 'node', 'data-id': n.id, style: `--kind: var(--k-${n.kind}); --d: ${delay(n.id)}ms` }, outer)
    // Concentric corners: ring radius = card radius (12) + gap (4).
    el('rect', { class: 'card', width: W, height: H, rx: 6 }, gn)
    // A thin stripe on the leading edge carries the kind; the glyph and
    // label say it in words.
    el('path', { class: 'stripe', d: `M6 0h-0a6 6 0 0 0-6 6v${H - 12}a6 6 0 0 0 6 6z` }, gn)
    el('path', { class: 'glyph', d: GLYPH[n.kind] || GLYPH.other, transform: 'translate(19,17)' }, gn)
    el('text', { class: 'kind', x: 30, y: 21 }, gn, (KIND[n.kind] || 'Step') + (n.kind === 'loop' && n.children ? ` · ${n.children.length} steps` : ''))
    el('text', { class: 'idx', x: W - 12, y: 21, 'text-anchor': 'end' }, gn, n.id)
    const t = el('text', { class: 'name', x: 14, y: 49 }, gn)
    wrap(n.name, 27).forEach((line, i) => el('tspan', { x: 14, dy: i ? 18 : 0 }, t, line))
    const title = el('title', {}, gn, `${n.id} · ${n.control}${n.milestone ? ' · ' + n.milestone : ''}\n${n.name}`)
    // State marks: colour and an icon, so the state never relies on motion.
    const icon = (cls, path) => {
      const s = el('g', { class: 'state ' + cls, transform: `translate(${W - 18},${H - 18})` }, gn)
      el('circle', { r: 8 }, s)
      el('path', { d: path }, s)
    }
    icon('s-done', 'M-4 0l3 3 5-6')
    icon('s-fail', 'M-3.5-3.5l7 7M3.5-3.5l-7 7')
    icon('s-wait', 'M0-4v4l3 2')
    icon('s-stuck', 'M0-4.5v5M0 3.5v.5')
    const badge = el('g', { class: 'badge', transform: `translate(${W - 34},-10)` }, gn)
    el('rect', { width: 34, height: 18, rx: 9 }, badge)
    el('text', { x: 17, y: 12.5, 'text-anchor': 'middle' }, badge, String(M.coverage.visits[n.id] || 0))
    nodeEl[n.id] = gn
    void title
  })

  // ---------------------------------------------------------------- milestone bands
  // Faint horizontal bands show which stage of the service each part of the
  // diagram belongs to.
  ;(() => {
    const spans = new Map()
    for (const n of M.nodes) {
      if (!n.milestone) continue
      const p = g.node(n.id)
      const s = spans.get(n.milestone) || { top: Infinity, bottom: -Infinity }
      s.top = Math.min(s.top, p.y - H / 2 - 24)
      s.bottom = Math.max(s.bottom, p.y + H / 2 + 24)
      spans.set(n.milestone, s)
    }
    const list = [...spans.entries()].sort((a, b) => a[1].top - b[1].top)
    let prev = -Infinity
    list.forEach(([name, s], i) => {
      const top = Math.max(s.top, prev)
      if (s.bottom <= top) return
      prev = s.bottom
      el('rect', { class: 'band-bg' + (i % 2 ? ' alt' : ''), x: 0, y: top, width: g.graph().width, height: s.bottom - top, rx: 18 }, $('bands'))
      // Vertical label in the left margin, like a swimlane.
      el('text', { class: 'band-label', 'text-anchor': 'end', transform: `translate(34,${top + 20}) rotate(-90)` }, $('bands'), name)
    })
  })()

  // ---------------------------------------------------------------- edges
  function pathFor(pts) {
    let d = `M${pts[0].x},${pts[0].y}`
    for (let i = 1; i < pts.length - 1; i++) {
      const mx = (pts[i].x + pts[i + 1].x) / 2, my = (pts[i].y + pts[i + 1].y) / 2
      d += ` Q${pts[i].x},${pts[i].y} ${mx},${my}`
    }
    const last = pts[pts.length - 1]
    return d + ` L${last.x},${last.y}`
  }

  const edgeEl = {}, labelEl = {}
  M.edges.forEach((e, i) => {
    const ed = g.edge({ v: e.from, w: e.to, name: 'e' + i })
    if (!ed) return
    const key = e.from + '>' + e.to
    const path = el('path', { class: 'edge', d: pathFor(ed.points), 'marker-end': 'url(#arrow)' }, $('edges'))
    path.style.setProperty('--d', delay(e.from) + 80 + 'ms')
    el('title', {}, path, e.condition || 'always')
    if (!edgeEl[key]) edgeEl[key] = path
    if (e.label) {
      const lg = el('g', { class: 'elabel', transform: `translate(${ed.x},${ed.y})` }, $('labels'))
      const w = ed.width
      el('rect', { x: -w / 2, y: -9, width: w, height: 18, rx: 3 }, lg)
      el('text', { x: 0, y: 4, 'text-anchor': 'middle' }, lg, e.label)
      el('title', {}, lg, e.condition)
      if (!labelEl[key]) labelEl[key] = lg
    }
  })
  for (const p of document.querySelectorAll('.edge')) p.style.setProperty('--len', Math.ceil(p.getTotalLength()))

  // ---------------------------------------------------------------- camera
  const cam = { x: 0, y: 0, k: 1 }
  const target = { x: 0, y: 0, k: 1 }
  let animating = false
  const mini = $('mini'), miniView = $('mini-view')
  mini.setAttribute('viewBox', `0 0 ${g.graph().width} ${g.graph().height}`)
  function apply() {
    viewport.setAttribute('transform', `translate(${cam.x},${cam.y}) scale(${cam.k})`)
    const r = stageBox()
    miniView.setAttribute('x', -cam.x / cam.k)
    miniView.setAttribute('y', -cam.y / cam.k)
    miniView.setAttribute('width', r.width / cam.k)
    miniView.setAttribute('height', (r.height - dockSpace) / cam.k)
  }
  function tick() {
    const e = reduced ? 1 : 0.16
    cam.x += (target.x - cam.x) * e
    cam.y += (target.y - cam.y) * e
    cam.k += (target.k - cam.k) * e
    apply()
    if (Math.abs(target.x - cam.x) + Math.abs(target.y - cam.y) + Math.abs(target.k - cam.k) * 100 > 0.3) requestAnimationFrame(tick)
    else { Object.assign(cam, target); apply(); animating = false }
  }
  function moveTo(x, y, k, instant) {
    Object.assign(target, { x, y, k })
    if (instant) { Object.assign(cam, target); apply(); return }
    if (!animating) { animating = true; requestAnimationFrame(tick) }
  }
  const stageBox = () => svg.getBoundingClientRect()
  const dockSpace = 150
  function fit(instant) {
    const r = stageBox(), gw = g.graph().width, gh = g.graph().height
    const k = Math.min((r.width - 40) / gw, (r.height - dockSpace - 40) / gh, 1.1)
    moveTo((r.width - gw * k) / 2, (r.height - dockSpace - gh * k) / 2 + 10, k, instant)
  }
  function focusNode(id) {
    if (!$('t-follow').checked) return
    const p = g.node(id), r = stageBox()
    const k = Math.max(target.k, 0.85)
    moveTo(r.width / 2 - p.x * k, (r.height - dockSpace) / 2 - p.y * k, k)
  }
  svg.addEventListener('wheel', (ev) => {
    ev.preventDefault()
    const r = stageBox(), px = ev.clientX - r.left, py = ev.clientY - r.top
    const k = Math.min(2.5, Math.max(0.12, target.k * Math.exp(-ev.deltaY * 0.0015)))
    const s = k / target.k
    moveTo(px - (px - target.x) * s, py - (py - target.y) * s, k, true)
  }, { passive: false })
  let drag = null
  svg.addEventListener('pointerdown', (ev) => { drag = { x: ev.clientX - target.x, y: ev.clientY - target.y }; svg.classList.add('dragging'); svg.setPointerCapture(ev.pointerId) })
  svg.addEventListener('pointermove', (ev) => { if (drag) moveTo(ev.clientX - drag.x, ev.clientY - drag.y, target.k, true) })
  svg.addEventListener('pointerup', () => { drag = null; svg.classList.remove('dragging') })
  // Click or drag the minimap to move there.
  const miniJump = (ev) => {
    const r = mini.getBoundingClientRect(), gw = g.graph().width, gh = g.graph().height
    const scale = Math.min(r.width / gw, r.height / gh)
    const ox = (r.width - gw * scale) / 2, oy = (r.height - gh * scale) / 2
    const gx = (ev.clientX - r.left - ox) / scale, gy = (ev.clientY - r.top - oy) / scale
    const sr = stageBox()
    moveTo(sr.width / 2 - gx * target.k, (sr.height - dockSpace) / 2 - gy * target.k, target.k, ev.type === 'pointermove')
  }
  let miniDrag = false
  mini.addEventListener('pointerdown', (ev) => { miniDrag = true; mini.setPointerCapture(ev.pointerId); miniJump(ev) })
  mini.addEventListener('pointermove', (ev) => { if (miniDrag) miniJump(ev) })
  mini.addEventListener('pointerup', () => { miniDrag = false })
  $('z-in').onclick = () => zoomBy(1.25)
  $('z-out').onclick = () => zoomBy(0.8)
  $('z-fit').onclick = () => fit()
  function zoomBy(f) {
    const r = stageBox(), cx = r.width / 2, cy = (r.height - dockSpace) / 2
    const k = Math.min(2.5, Math.max(0.12, target.k * f)), s = k / target.k
    moveTo(cx - (cx - target.x) * s, cy - (cy - target.y) * s, k)
  }

  // ---------------------------------------------------------------- sidebar
  $('wf-name').textContent = M.workflow
  $('wf-meta').textContent = `v${M.version} · ${M.mode === 'each' ? 'every answer tried' : 'every combination'}`
  const cov = M.coverage
  const stat = (dt, dd) => { const d = document.createElement('div'); d.innerHTML = `<dt>${dt}</dt><dd>${dd}</dd>`; $('stats').appendChild(d) }
  stat('Scenarios', M.scenarios.length)
  stat('Reached', `${cov.nodesHit}/${cov.nodes}`)
  stat('Branches', `${cov.edgesTaken}/${cov.edges}`)
  if (M.note) { $('wf-note').hidden = false; $('wf-note').textContent = M.note }

  // "n204: lookup finds nothing" -> "Registry Check: found nothing"
  function human(d) {
    const m = d.match(/^(n\d+)(?:\/(\w+))?( \((?:visit|item) \d+\))?: (.*)$/)
    if (!m || !byId[m[1]]) return d
    const what = m[4]
      .replace(/^officer /, '')
      .replace(/^lookup finds nothing$/, 'found nothing')
      .replace(/^lookup finds a row$/, 'found a match')
      .replace(/^lookup /, '')
    const name = byId[m[1]].name + (m[2] ? ' › ' + m[2] : '')
    return `${name}${m[3] || ''}: ${what}`
  }
  const humanAll = (list) => list.map(human).join(' · ')

  const scById = {}
  M.scenarios.forEach((s) => (scById[s.id] = s))
  const itemEls = {}
  function item(parent, s, label, sub, cls) {
    const li = document.createElement('li')
    const b = document.createElement('button')
    b.type = 'button'
    b.className = 'item ' + (cls || '')
    b.innerHTML = `<span class="t"><span class="dot ${s.status}"></span><span></span><span class="id">${s.id}</span></span><span class="d"></span>`
    b.querySelector('.t span:nth-child(2)').textContent = label
    b.querySelector('.d').textContent = sub
    b.onclick = () => { touched = true; select(s.id, true) }
    li.appendChild(b)
    parent.appendChild(li)
    ;(itemEls[s.id] = itemEls[s.id] || []).push(b)
  }
  if (M.findings && M.findings.length) {
    $('findings').hidden = false
    for (const f of M.findings) {
      const s = scById[f.example]
      const where = byId[f.at] ? byId[f.at].name : f.at
      item($('finding-list'), s, `${f.status === 'failed' ? 'Fails at' : 'Stuck at'} ${where}`, `${f.reason} · ${f.scenarios === 1 ? '1 scenario' : f.scenarios + ' scenarios'}`, 'finding')
    }
  }
  // Group scenarios: problems first, then by the status the applicant ends on.
  const groups = new Map()
  const order = { failed: 0, stuck: 1, runaway: 1, waiting: 2, completed: 3 }
  // Each problem's example is already listed under Problems; list it once.
  const examples = new Set((M.findings || []).map((f) => f.example))
  ;[...M.scenarios].sort((a, b) => (order[a.status] ?? 4) - (order[b.status] ?? 4)).forEach((s) => {
    if (examples.has(s.id)) return
    const key = s.status === 'completed' ? `Ends: ${s.title}` : s.status === 'waiting' ? 'Not explored further' : 'More routes into these problems'
    if (!groups.has(key)) groups.set(key, [])
    groups.get(key).push(s)
  })
  for (const [name, list] of groups) {
    const h = document.createElement('h3')
    h.innerHTML = `<span class="dot ${list[0].status}"></span>`
    h.append(`${name} (${list.length})`)
    $('scenario-groups').appendChild(h)
    const ul = document.createElement('ul')
    ul.className = 'list'
    $('scenario-groups').appendChild(ul)
    list.forEach((s) => item(ul, s, s.title, s.decisions.length ? humanAll(s.decisions) : 'No decisions: one way through'))
  }

  // ---------------------------------------------------------------- playback
  let sc = null, k = 0, playing = false, runId = 0
  let touched = false // the viewer has picked or moved something; never override that
  const speed = () => parseFloat($('p-speed').value)
  const sleep = (ms, id) => new Promise((res) => setTimeout(() => res(id === runId), reduced ? 0 : ms / speed()))

  const finalClass = (ev) => ({ ran: 'done', ended: 'done', failed: 'failed', waiting: 'waiting', stuck: 'stuck' }[ev] || 'done')

  function clearState() {
    for (const id in nodeEl) nodeEl[id].classList.remove('running', 'done', 'failed', 'waiting', 'stuck', 'visited')
    for (const key in edgeEl) { edgeEl[key].classList.remove('taken'); edgeEl[key].setAttribute('marker-end', 'url(#arrow)') }
    for (const key in labelEl) labelEl[key].classList.remove('taken')
    $('fx').replaceChildren()
  }
  function markNode(id, cls) {
    const n = nodeEl[id]
    if (!n) return
    n.classList.remove('running', 'done', 'failed', 'waiting', 'stuck')
    n.classList.add(cls, 'visited')
  }
  function markEdge(from, to) {
    const key = from + '>' + to
    if (edgeEl[key]) { edgeEl[key].classList.add('taken'); edgeEl[key].setAttribute('marker-end', 'url(#arrow-on)') }
    if (labelEl[key]) labelEl[key].classList.add('taken')
  }

  // What happened at a step, in a few words, for the callout on the card.
  function outcome(s) {
    const n = byId[s.node] || {}
    if (s.event === 'failed') return { text: 'Fails here', bad: true }
    if (s.event === 'stuck') return { text: 'Stuck: no branch matches', bad: true }
    if (s.event === 'waiting') return { text: 'Waiting for an answer', bad: false, wait: true }
    if (n.kind === 'task' && s.detail && s.detail.includes('→')) return { text: s.detail.split('→').pop().trim() }
    if (n.kind === 'lookup' && s.detail) return { text: /: no rows/.test(s.detail) ? 'Found nothing' : 'Found a match' }
    if (n.kind === 'loop' && s.loop) return { text: s.loop === 1 ? 'Ran once' : s.loop === 2 ? 'Ran twice' : `Ran ${s.loop} times` }
    return null
  }
  function callout(x, y, text, cls) {
    const outer = el('g', { transform: `translate(${x},${y})` }, $('fx'))
    const g2 = el('g', { class: 'callout ' + (cls || '') }, outer)
    const t = el('text', { x: 12, y: 15.5 }, g2, text)
    const w = Math.min(t.getComputedTextLength() + 22, 320)
    g2.insertBefore(el('rect', { width: w, height: 23, rx: 4 }), t)
    g2.insertBefore(el('rect', { class: 'rule', width: 3, height: 23, rx: 1.5 }), t)
    return outer
  }
  function showOutcome(s) {
    const o = outcome(s)
    if (!o) return
    const p = g.node(s.node)
    if (!p) return
    callout(p.x + W / 2 + 10, p.y - 11, o.text, o.bad ? 'bad' : o.wait ? 'wait' : '')
  }
  function showStatus(s) {
    if (!s.status || !s.next) return
    const ed = edgeEl[s.node + '>' + s.next]
    if (!ed) return
    // Two thirds along, beside the line, so it does not sit on the condition label.
    const at = ed.getPointAtLength(ed.getTotalLength() * 0.7)
    callout(at.x + 14, at.y - 11, 'Applicant sees: ' + s.status, 'status')
  }

  function caption(s) {
    if (!s) { $('cap-kind').textContent = ''; $('cap-text').textContent = 'Pick a scenario to play it on the workflow.'; return }
    const n = byId[s.node] || { name: s.node, kind: 'other' }
    const plain = $('t-plain').checked
    $('cap-kind').textContent = (KIND[n.kind] || 'Step') + (plain ? '' : ' · ' + s.node)
    const cap = $('cap-text')
    cap.replaceChildren()
    const b = document.createElement('b')
    b.textContent = n.name
    cap.append(b)
    let detail = s.detail || ''
    if (s.event === 'failed' || s.event === 'stuck') detail = (s.event === 'failed' ? 'The instance would fail here. ' : 'No transition matches; the case stops here. ') + detail.split('\n')[0]
    if (detail) cap.append(' — ' + detail.replace(/\s+/g, ' ').slice(0, 220))
  }
  // At the end, step back to show the whole route that lit up.
  function fitRoute() {
    if (!$('t-follow').checked || !sc) return
    let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity
    for (const s of sc.steps) {
      const p = g.node(s.node)
      if (!p) continue
      x0 = Math.min(x0, p.x - W / 2); y0 = Math.min(y0, p.y - H / 2)
      x1 = Math.max(x1, p.x + W / 2 + 160); y1 = Math.max(y1, p.y + H / 2)
    }
    const r = stageBox(), pad = 40
    const k = Math.min((r.width - pad * 2) / (x1 - x0), (r.height - dockSpace - pad * 2) / (y1 - y0), 1)
    if (k < 0.45) return // too long to read whole; stay where the story ends
    moveTo((r.width - (x1 - x0) * k) / 2 - x0 * k, (r.height - dockSpace - (y1 - y0) * k) / 2 - y0 * k, k)
  }
  function endCaption() {
    $('cap-kind').textContent = sc.status
    const t = $('cap-text')
    t.replaceChildren()
    const b = document.createElement('b')
    b.textContent = sc.status === 'completed' ? `Done. The applicant ends on “${sc.title}”.` : sc.title + '.'
    t.append(b)
    if (sc.reason && sc.status !== 'completed') t.append(' ' + sc.reason.slice(0, 200))
  }

  function applyUpTo(n) {
    clearState()
    for (let i = 0; i < n; i++) {
      const s = sc.steps[i]
      markNode(s.node, finalClass(s.event))
      if (s.next && i < n - 1) markEdge(s.node, s.next)
      if (s.next && i === n - 1 && n === sc.steps.length) markEdge(s.node, s.next)
      const o = outcome(s), kind = (byId[s.node] || {}).kind
      if (o && (kind === 'task' || kind === 'lookup' || o.bad || i === n - 1)) showOutcome(s)
      if (s.status && i < n - 1) showStatus(s)
    }
    k = n
    sync()
    caption(sc.steps[n - 1])
    if (n === sc.steps.length) endCaption()
    if (n > 0) focusNode(sc.steps[n - 1].node)
  }

  function sync() {
    $('p-scrub').max = sc ? sc.steps.length : 0
    $('p-scrub').value = k
    $('p-count').textContent = `${k} / ${sc ? sc.steps.length : 0}`
    $('p-play').classList.toggle('playing', playing)
    // Autoplay would announce every step; speak only when paused or stepping.
    $('caption').setAttribute('aria-live', playing ? 'off' : 'polite')
    $('p-play').setAttribute('aria-label', playing ? 'Pause' : 'Play')
  }

  function travel(from, to, id) {
    const path = edgeEl[from + '>' + to]
    if (!path || reduced) return Promise.resolve(true)
    const len = path.getTotalLength()
    const dur = Math.min(Math.max(len / 0.42, 320), 1300) / speed()
    // A comet: the head, and a short trail of fading dots behind it.
    const trail = [0.06, 0.04, 0.02].map((lag, i) => ({ lag, c: el('circle', { class: 'trail', r: 3.6 - i * 0.8, opacity: 0.35 - i * 0.1 }, $('fx')) }))
    const tok = el('circle', { class: 'token', r: 5.5 }, $('fx'))
    const done = () => { tok.remove(); trail.forEach((t) => t.c.remove()) }
    return new Promise((res) => {
      const t0 = performance.now()
      const ease = (t) => (t < 0.5 ? 2 * t * t : 1 - Math.pow(-2 * t + 2, 2) / 2)
      const frame = (now) => {
        if (id !== runId) { done(); return res(false) }
        const t = Math.min((now - t0) / dur, 1)
        const p = path.getPointAtLength(len * ease(t))
        tok.setAttribute('cx', p.x)
        tok.setAttribute('cy', p.y)
        for (const tr of trail) {
          const q = path.getPointAtLength(len * ease(Math.max(0, t - tr.lag)))
          tr.c.setAttribute('cx', q.x)
          tr.c.setAttribute('cy', q.y)
        }
        if (t < 1) requestAnimationFrame(frame)
        else { done(); res(true) }
      }
      requestAnimationFrame(frame)
    })
  }

  async function play() {
    if (!sc) return
    if (k >= sc.steps.length) applyUpTo(0)
    playing = true
    const id = ++runId
    sync()
    // Drop the transient callout of the last step before carrying on.
    while (playing && id === runId && k < sc.steps.length) {
      const s = sc.steps[k]
      focusNode(s.node)
      markNode(s.node, 'running')
      caption(s)
      if (!(await sleep(520, id))) return
      markNode(s.node, finalClass(s.event))
      const kind = (byId[s.node] || {}).kind
      showOutcome(s)
      if (!(await sleep(kind === 'task' || kind === 'lookup' || s.event !== 'ran' ? 650 : 220, id))) return
      if (s.next) {
        if (!(await travel(s.node, s.next, id))) return
        markEdge(s.node, s.next)
        showStatus(s)
      }
      k++
      sync()
    }
    if (id === runId) {
      playing = false
      sync()
      if (k >= sc.steps.length) { endCaption(); setTimeout(fitRoute, reduced ? 0 : 500) }
    }
  }
  function pause() { playing = false; runId++; sync() }

  function select(id, autoplay) {
    pause()
    sc = scById[id]
    svg.classList.add('story')
    for (const key in itemEls) itemEls[key].forEach((b) => b.setAttribute('aria-current', key === id ? 'true' : 'false'))
    applyUpTo(0)
    caption(null)
    $('cap-text').textContent = sc.decisions.length ? humanAll(sc.decisions) : 'One way through: no decisions on this route.'
    if (autoplay) play()
  }

  $('p-play').onclick = () => { touched = true; playing ? pause() : play() }
  $('p-next').onclick = () => { touched = true; if (!sc) return; pause(); applyUpTo(Math.min(k + 1, sc.steps.length)) }
  $('p-back').onclick = () => { touched = true; if (!sc) return; pause(); applyUpTo(Math.max(k - 1, 0)) }
  // Read the slider before pause(): pause re-syncs it to the current step.
  $('p-scrub').oninput = (e) => { touched = true; const to = +e.target.value; if (!sc) return; pause(); applyUpTo(to) }
  $('t-coverage').onchange = (e) => svg.classList.toggle('cov', e.target.checked)
  $('t-plain').onchange = (e) => { svg.classList.toggle('plain', e.target.checked); if (sc && k) caption(sc.steps[k - 1]) }
  document.addEventListener('keydown', (e) => {
    if (e.target.closest && e.target.closest('input, select, textarea')) return
    if (e.key === ' ') { e.preventDefault(); $('p-play').click() }
    else if (e.key === 'ArrowRight') $('p-next').click()
    else if (e.key === 'ArrowLeft') $('p-back').click()
    else if (e.key === 'f') fit()
  })

  // Coverage marks.
  for (const n of M.nodes) if (!M.coverage.visits[n.id]) nodeEl[n.id].classList.add('cold')
  for (const key of M.coverage.untaken || []) if (edgeEl[key]) edgeEl[key].classList.add('untaken')

  // ---------------------------------------------------------------- start
  // Start readable: if the whole workflow would be too small, open at the
  // top on the Start node instead (Fit still shows everything).
  function opening() {
    const r = stageBox(), gw = g.graph().width, gh = g.graph().height
    const kFit = Math.min((r.width - 40) / gw, (r.height - dockSpace - 40) / gh, 1.1)
    if (kFit >= 0.45) return fit(true)
    const start = M.nodes.find((n) => n.kind === 'start') || M.nodes[0]
    const p = g.node(start.id), k = 0.75
    moveTo(r.width / 2 - p.x * k, 60 - (p.y - H / 2) * k, k, true)
  }
  opening()
  // Open on the first problem; with none, on the longest way through that
  // ends well, which shows the most of the workflow.
  const longest = [...M.scenarios].filter((s) => s.status === 'completed').sort((a, b) => b.steps.length - a.steps.length)[0]
  const first = (M.findings && M.findings.length && M.findings[0].example) || (longest && longest.id) || (M.scenarios[0] && M.scenarios[0].id)
  if (!reduced) {
    svg.classList.add('intro')
    const total = Math.min(ys.length * 45, 1400) + 600
    setTimeout(() => { svg.classList.remove('intro'); if (first && !touched) select(first, true) }, total)
  } else if (first) {
    select(first, false)
  }
})()
