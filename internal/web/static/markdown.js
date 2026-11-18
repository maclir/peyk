// Renders the subset of Markdown agents write in replies: headings,
// paragraphs, lists, tables, fenced code, inline code, bold, italics and links.
// Everything is escaped first, so a reply can never inject markup.

const escapeHTML = (s) =>
  s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);

function inline(text) {
  const codes = [];
  let s = escapeHTML(text).replace(/`([^`]+)`/g, (_, code) => {
    codes.push(code);
    return `\u0000${codes.length - 1}\u0000`;
  });
  s = s
    .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
    .replace(/(^|[^*\w])\*([^*\s][^*]*)\*(?!\w)/g, "$1<em>$2</em>")
    .replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, '<a href="$2" rel="noreferrer noopener" target="_blank">$1</a>');
  return s.replace(/\u0000(\d+)\u0000/g, (_, i) => `<code>${codes[Number(i)]}</code>`);
}

// cells splits a table row on unescaped pipes, dropping the optional outer
// pipes and unescaping \| inside cells.
function cells(line) {
  let row = line.trim();
  if (row.startsWith("|")) row = row.slice(1);
  if (row.endsWith("|") && !row.endsWith("\\|")) row = row.slice(0, -1);
  return row.split(/(?<!\\)\|/).map((c) => c.trim().replace(/\\\|/g, "|"));
}

const delimiterRow = /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/;

function alignment(cell) {
  if (cell.startsWith(":") && cell.endsWith(":")) return "center";
  if (cell.endsWith(":")) return "right";
  if (cell.startsWith(":")) return "left";
  return "";
}

function renderTable(header, aligns, rows) {
  const cell = (tag, text, i) => {
    const cls = aligns[i] ? ` class="${aligns[i]}"` : "";
    return `<${tag}${cls}>${inline(text ?? "")}</${tag}>`;
  };
  const row = (tag, texts) => `<tr>${aligns.map((_, i) => cell(tag, texts[i], i)).join("")}</tr>`;
  const body = rows.length ? `<tbody>${rows.map((r) => row("td", r)).join("")}</tbody>` : "";
  return `<div class="table"><table><thead>${row("th", header)}</thead>${body}</table></div>`;
}

const indentOf = (line) => line.match(/^[ \t]*/)[0].replace(/\t/g, "    ").length;

function renderList(list) {
  const start = list.tag === "ol" && list.start !== 1 ? ` start="${list.start}"` : "";
  const items = list.items.map((item) => `<li>${inline(item.text)}${item.lists.map(renderList).join("")}</li>`);
  return `<${list.tag}${start}>${items.join("")}</${list.tag}>`;
}

export function renderMarkdown(src) {
  const lines = String(src ?? "").replace(/\r\n?/g, "\n").split("\n");
  const out = [];
  let para = [];
  // Open lists from outermost to innermost; each holds the indent of its
  // markers so a deeper marker nests and a shallower one closes lists.
  let lists = [];
  let blank = false;

  const flushPara = () => {
    if (para.length) out.push(`<p>${inline(para.join(" "))}</p>`);
    para = [];
  };
  const flushList = () => {
    if (lists.length) out.push(renderList(lists[0]));
    lists = [];
  };
  const lastItem = () => {
    const items = lists[lists.length - 1].items;
    return items[items.length - 1];
  };
  const addItem = (tag, indent, start, text) => {
    // Agents nest with two spaces even under "1. ", so any deeper marker nests.
    while (lists.length > 1 && lists[lists.length - 1].indent > indent) lists.pop();
    const top = lists[lists.length - 1];
    if (top && (top.indent > indent || (top.indent === indent && top.tag !== tag))) {
      if (lists.length === 1) flushList();
      else lists.pop();
    }
    let list = lists[lists.length - 1];
    if (!list || list.indent < indent) {
      const parent = list && lastItem();
      if (!parent) flushList();
      list = { tag, indent, start, items: [] };
      if (parent) parent.lists.push(list);
      lists.push(list);
    }
    list.items.push({ text, lists: [] });
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const fence = line.match(/^\s*```/);
    if (fence) {
      flushPara();
      flushList();
      const code = [];
      for (i++; i < lines.length && !/^\s*```/.test(lines[i]); i++) code.push(lines[i]);
      out.push(`<pre><code>${escapeHTML(code.join("\n"))}</code></pre>`);
      continue;
    }
    if (line.includes("|") && i + 1 < lines.length && delimiterRow.test(lines[i + 1])) {
      const header = cells(line);
      const aligns = cells(lines[i + 1]).map(alignment);
      if (header.length === aligns.length) {
        flushPara();
        flushList();
        const rows = [];
        for (i += 2; i < lines.length && lines[i].includes("|") && lines[i].trim() !== ""; i++) rows.push(cells(lines[i]));
        i--;
        out.push(renderTable(header, aligns, rows));
        continue;
      }
    }
    const heading = line.match(/^(#{1,6})\s+(.*)$/);
    const bullet = line.match(/^\s*[-*+]\s+(.*)$/);
    const numbered = line.match(/^\s*(\d+)[.)]\s+(.*)$/);
    const wasBlank = blank;
    blank = false;
    if (heading) {
      flushPara();
      flushList();
      const level = Math.min(heading[1].length + 2, 6);
      out.push(`<h${level}>${inline(heading[2])}</h${level}>`);
    } else if (bullet) {
      flushPara();
      addItem("ul", indentOf(line), 1, bullet[1]);
    } else if (numbered) {
      flushPara();
      addItem("ol", indentOf(line), Number(numbered[1]), numbered[2]);
    } else if (line.trim() === "") {
      flushPara();
      blank = true;
    } else if (lists.length && /^\s{2,}\S/.test(line)) {
      lastItem().text += " " + line.trim();
    } else if (lists.length && !wasBlank) {
      lastItem().text += " " + line.trim();
    } else {
      flushList();
      para.push(line.trim());
    }
  }
  flushPara();
  flushList();
  return out.join("\n");
}
