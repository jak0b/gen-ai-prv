const input = document.querySelector("#file-input");
const form = input.form;
const summary = document.querySelector("#file-summary");
const count = document.querySelector("#file-count");
const list = document.querySelector("#file-list");
const submit = form.querySelector('button[type="submit"]');
let selectedFiles = [];

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
  selectedFiles = Array.from(input.files);
  list.replaceChildren();

  if (selectedFiles.length === 0) {
    summary.hidden = true;
    submit.disabled = true;
    return;
  }

  for (const [index, file] of selectedFiles.entries()) {
    const item = document.createElement("li");
    const choice = document.createElement("label");
    const checkbox = document.createElement("input");
    const name = document.createElement("span");
    const meta = document.createElement("span");

    choice.className = "file-choice";
    checkbox.type = "checkbox";
    checkbox.checked = true;
    checkbox.dataset.fileIndex = index;
    checkbox.setAttribute("aria-label", `Include ${file.name} in check`);
    name.className = "file-name";
    name.textContent = file.name;
    meta.className = "file-meta";
    meta.textContent = `${fileType(file)} · ${formatSize(file.size)}`;
    choice.append(checkbox, name, meta);
    item.append(choice);
    list.append(item);
  }
  summary.hidden = false;
  updateSelectionCount();
});

list.addEventListener("change", updateSelectionCount);

form.addEventListener("formdata", (event) => {
  event.formData.delete(input.name);
  for (const checkbox of list.querySelectorAll('input[type="checkbox"]:checked')) {
    event.formData.append(input.name, selectedFiles[Number(checkbox.dataset.fileIndex)]);
  }
});

function updateSelectionCount() {
  const selectedCount = list.querySelectorAll('input[type="checkbox"]:checked').length;
  count.textContent = `${selectedCount} of ${selectedFiles.length} selected`;
  submit.disabled = selectedCount === 0;
}
