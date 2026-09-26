import svgPanZoom from 'svg-pan-zoom';

let activePanZoom = null;

function createLightboxModal() {
  const existing = document.querySelector('.mermaid-lightbox-dialog');
  if (existing) {
    return existing;
  }

  const dialog = document.createElement('dialog');
  dialog.className = 'mermaid-lightbox-dialog';
  dialog.innerHTML = `
    <div class="lightbox-header">
      <span class="lightbox-title">Diagram Viewer</span>
      <span class="lightbox-hint">Scroll/pinch to zoom • Click and drag to pan • Double click to reset</span>
      <button class="lightbox-close-btn" type="button" aria-label="Close dialog">✕ Close (Esc)</button>
    </div>
    <div class="lightbox-body">
      <div class="lightbox-svg-container"></div>
    </div>
  `;

  document.body.appendChild(dialog);

  const closeBtn = dialog.querySelector('.lightbox-close-btn');
  closeBtn.addEventListener('click', () => closeLightbox(dialog));

  dialog.addEventListener('click', (e) => {
    if (e.target === dialog) {
      closeLightbox(dialog);
    }
  });

  dialog.addEventListener('close', () => {
    if (activePanZoom) {
      try {
        activePanZoom.destroy();
      } catch (_) {}
      activePanZoom = null;
    }
    const container = dialog.querySelector('.lightbox-svg-container');
    if (container) {
      container.innerHTML = '';
    }
  });

  return dialog;
}

function openLightbox(originalSvg) {
  const dialog = createLightboxModal();
  const container = dialog.querySelector('.lightbox-svg-container');
  container.innerHTML = '';

  // Clone SVG
  const clonedSvg = originalSvg.cloneNode(true);
  clonedSvg.removeAttribute('id');
  clonedSvg.style.width = '100%';
  clonedSvg.style.height = '100%';
  clonedSvg.style.maxWidth = 'none';
  clonedSvg.style.maxHeight = 'none';

  container.appendChild(clonedSvg);
  dialog.showModal();

  // Initialize pan-zoom on cloned SVG
  requestAnimationFrame(() => {
    try {
      activePanZoom = svgPanZoom(clonedSvg, {
        zoomEnabled: true,
        controlIconsEnabled: true,
        fit: true,
        center: true,
        minZoom: 0.5,
        maxZoom: 15,
        zoomScaleSensitivity: 0.3,
      });
      activePanZoom.resize();
      activePanZoom.fit();
      activePanZoom.center();
    } catch (err) {
      console.warn('[mermaid-lightbox] svgPanZoom initialization failed:', err);
    }
  });
}

function closeLightbox(dialog) {
  dialog.close();
}

function attachLightboxToDiagrams() {
  const diagrams = document.querySelectorAll('pre.mermaid[data-processed]:not([data-lightbox-init])');

  diagrams.forEach((pre) => {
    const svg = pre.querySelector('svg');
    if (!svg) return;

    pre.setAttribute('data-lightbox-init', 'true');
    pre.classList.add('lightbox-enabled');

    // Create expand button
    const btn = document.createElement('button');
    btn.className = 'mermaid-expand-btn';
    btn.setAttribute('type', 'button');
    btn.setAttribute('aria-label', 'Expand diagram fullscreen');
    btn.innerHTML = `
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
        <polyline points="15 3 21 3 21 9"></polyline>
        <polyline points="9 21 3 21 3 15"></polyline>
        <line x1="21" y1="3" x2="14" y2="10"></line>
        <line x1="3" y1="21" x2="10" y2="14"></line>
      </svg>
      <span>Expand</span>
    `;

    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      openLightbox(svg);
    });

    pre.addEventListener('click', () => {
      openLightbox(svg);
    });

    pre.appendChild(btn);
  });
}

// Observe mutations for newly rendered diagrams
function setupObserver() {
  const observer = new MutationObserver(() => {
    attachLightboxToDiagrams();
  });

  observer.observe(document.body, {
    childList: true,
    subtree: true,
    attributes: true,
    attributeFilter: ['data-processed'],
  });

  attachLightboxToDiagrams();
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', setupObserver);
  } else {
    setupObserver();
  }

  document.addEventListener('astro:after-swap', () => {
    attachLightboxToDiagrams();
  });
}
