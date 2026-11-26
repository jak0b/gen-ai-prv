const input = document.querySelector("#file-input");
const summary = document.querySelector("#file-summary");
const count = document.querySelector("#file-count");
const list = document.querySelector("#file-list");

function formatSize(bytes) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}

function fileType(file) {
  if (file.type) return file.type;
  const extension = file.name.split(".").pop();
  return extension && extension !== file.name ? `${extension.toUpperCase()} file` : "Unknown file type";
}

input.addEventListener("change", () => {
  const files = Array.from(input.files);
  list.replaceChildren();

  if (files.length === 0) {
    summary.hidden = true;
    return;
  }

  count.textContent = `${files.length} ${files.length === 1 ? "file" : "files"}`;
  for (const file of files) {
    const item = document.createElement("li");
    const name = document.createElement("span");
    const meta = document.createElement("span");

    name.className = "file-name";
    name.textContent = file.name;
    meta.className = "file-meta";
    meta.textContent = `${fileType(file)} · ${formatSize(file.size)}`;
    item.append(name, meta);
    list.append(item);
  }
  summary.hidden = false;
});
