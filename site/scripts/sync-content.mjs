import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const repoRoot = path.resolve(__dirname, '../..');
const siteDir = path.resolve(__dirname, '..');
const docsDest = path.resolve(siteDir, 'src/content/docs');
const dataDest = path.resolve(siteDir, 'src/data');

// Clean and recreate docs destination
fs.rmSync(docsDest, { recursive: true, force: true });
fs.mkdirSync(docsDest, { recursive: true });
fs.mkdirSync(path.join(docsDest, 'guides'), { recursive: true });
fs.mkdirSync(path.join(docsDest, 'samples'), { recursive: true });
fs.mkdirSync(dataDest, { recursive: true });

// Map of source file (relative to repo root) -> { destFile (rel to docsDest), title, description, slug }
const fileMap = {
  'README.md': {
    dest: 'index.mdx',
    title: 'Overview',
    description: 'Pure-Go bindings for Moonshine on-device STT, streaming TTS, text embeddings, and voice agent cascade.',
    sitePath: '/moonshine-go/',
  },
  'BENCHMARKS.md': {
    dest: 'benchmarks.mdx',
    title: 'Benchmarks & Latency',
    description: 'Empirical latency waterfalls, throughput, and memory profiles for Moonshine in Go.',
    sitePath: '/moonshine-go/benchmarks/',
  },
  'CHANGELOG.md': {
    dest: 'changelog.md',
    title: 'Changelog',
    description: 'Release history and version notes for moonshine-go.',
    sitePath: '/moonshine-go/changelog/',
  },
  'docs/user-guide.md': {
    dest: 'guides/user-guide.md',
    title: 'User Guide & CLI',
    description: 'Complete reference for moonshine live, transcribe, serve, and tts subcommands.',
    sitePath: '/moonshine-go/guides/user-guide/',
  },
  'docs/hosting.md': {
    dest: 'guides/hosting.md',
    title: 'Hosting & Remote Clients',
    description: 'Network architecture, remote audio streaming, and deployment guidelines.',
    sitePath: '/moonshine-go/guides/hosting/',
  },
  'docs/bundling-libmoonshine.md': {
    dest: 'guides/bundling-libmoonshine.md',
    title: 'Bundling libmoonshine',
    description: 'Embedding and distributing libmoonshine shared libraries with your application.',
    sitePath: '/moonshine-go/guides/bundling-libmoonshine/',
  },
  'docs/hardware-acceleration.md': {
    dest: 'guides/hardware-acceleration.md',
    title: 'Hardware Acceleration',
    description: 'GPU and NPU acceleration configuration across macOS, Linux, and Windows.',
    sitePath: '/moonshine-go/guides/hardware-acceleration/',
  },
  'docs/faq.md': {
    dest: 'guides/faq.md',
    title: 'Frequently Asked Questions',
    description: 'Common questions about models, performance, and architecture.',
    sitePath: '/moonshine-go/guides/faq/',
  },
  'docs/testing-with-container.md': {
    dest: 'guides/testing-with-container.md',
    title: 'Testing with Containers',
    description: 'Hermetic containerized testing and smoke test setup.',
    sitePath: '/moonshine-go/guides/testing-with-container/',
  },
  'samples/GUIDE.md': {
    dest: 'samples/guide.md',
    title: 'Architectural Pattern Guide',
    description: 'Sidecar pattern rubric, latency waterfalls, and vertical application matrix.',
    sitePath: '/moonshine-go/samples/guide/',
  },
  'samples/TUTORIAL.md': {
    dest: 'samples/tutorial.md',
    title: 'AgentFlow Voice Agent Tutorial',
    description: 'Step-by-step guide to building conversational voice agents with pkg/agentflow.',
    sitePath: '/moonshine-go/samples/tutorial/',
  },
};

// Discover sample directories
const samplesDir = path.join(repoRoot, 'samples');
const sampleEntries = fs.readdirSync(samplesDir, { withFileTypes: true });
const sampleData = [];

for (const entry of sampleEntries) {
  if (!entry.isDirectory()) continue;
  const sampleName = entry.name;
  const sampleReadme = path.join('samples', sampleName, 'README.md');
  const fullReadmePath = path.join(repoRoot, sampleReadme);
  if (!fs.existsSync(fullReadmePath)) continue;

  const content = fs.readFileSync(fullReadmePath, 'utf-8');
  const rating = parseSampleRating(content, sampleName);
  sampleData.push(rating);

  fileMap[sampleReadme] = {
    dest: `samples/${sampleName}.md`,
    title: rating.title || sampleName,
    description: rating.description || `Sample ${sampleName} for moonshine-go`,
    sitePath: `/moonshine-go/samples/${sampleName}/`,
  };
}

// Write extracted sample metadata
fs.writeFileSync(path.join(dataDest, 'samples.json'), JSON.stringify(sampleData, null, 2), 'utf-8');
console.log(`[sync] Parsed metadata for ${sampleData.length} samples into src/data/samples.json`);

// Process each file in fileMap
let totalLinksRewritten = 0;
let errors = 0;

for (const [srcRel, meta] of Object.entries(fileMap)) {
  const srcFull = path.join(repoRoot, srcRel);
  if (!fs.existsSync(srcFull)) {
    console.error(`[sync] ERROR: Source file not found: ${srcRel}`);
    errors++;
    continue;
  }

  let content = fs.readFileSync(srcFull, 'utf-8');
  const srcDir = path.dirname(srcFull);

  // Link rewriting regex: [text](link)
  content = content.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (match, text, url) => {
    // Keep external URLs and anchor-only links
    if (/^(https?:|mailto:|#)/.test(url)) {
      return match;
    }

    // Split anchor if present
    const [rawPath, anchor] = url.split('#');
    if (!rawPath) {
      return match;
    }

    // Resolve target path relative to source directory
    const targetAbs = path.resolve(srcDir, rawPath);
    const targetRel = path.relative(repoRoot, targetAbs);

    // Validate target exists
    if (!fs.existsSync(targetAbs)) {
      console.warn(`[sync] Warning: Link target does not exist: ${srcRel} -> ${url} (resolved: ${targetRel})`);
      // Do not fail immediately on minor anchor/untracked links, but fallback to GitHub link
    }

    // Check if target matches one of the published site docs
    const normalizedTargetRel = targetRel.replace(/\\/g, '/');
    let matchedSitePath = null;

    if (fileMap[normalizedTargetRel]) {
      matchedSitePath = fileMap[normalizedTargetRel].sitePath;
    } else if (fileMap[`${normalizedTargetRel}/README.md`]) {
      matchedSitePath = fileMap[`${normalizedTargetRel}/README.md`].sitePath;
    }

    if (matchedSitePath) {
      totalLinksRewritten++;
      const fullUrl = anchor ? `${matchedSitePath}#${anchor}` : matchedSitePath;
      return `[${text}](${fullUrl})`;
    }

    // Non-published file (code, test asset, config): map to GitHub blob URL
    totalLinksRewritten++;
    const ghUrl = `https://github.com/ghchinoy/moonshine-go/blob/main/${normalizedTargetRel}${anchor ? `#${anchor}` : ''}`;
    return `[${text}](${ghUrl})`;
  });

  // Remove existing top H1 if it duplicates title in frontmatter
  let cleanContent = content;
  if (cleanContent.startsWith('# ')) {
    const firstLineEnd = cleanContent.indexOf('\n');
    cleanContent = cleanContent.slice(firstLineEnd + 1).trimStart();
  }

  let extraImports = '';
  if (meta.dest === 'index.mdx') {
    extraImports = "\nimport AudioShowcase from '../../components/AudioShowcase.astro';\n";
    cleanContent = cleanContent.replace('## Contents', '## Live Audio Demos\n\n<AudioShowcase />\n\n## Contents');
  } else if (meta.dest === 'benchmarks.mdx') {
    extraImports = "\nimport BenchmarkCharts from '../../components/BenchmarkCharts.astro';\n";
    cleanContent = cleanContent.replace('## 2. In-Process Micro-Benchmarks', '## Visual Performance Profile\n\n<BenchmarkCharts />\n\n## 2. In-Process Micro-Benchmarks');
  }

  // Inject frontmatter
  const frontmatter = `---
title: "${meta.title.replace(/"/g, '\\"')}"
description: "${meta.description.replace(/"/g, '\\"')}"
---
${extraImports}
`;

  const destFull = path.join(docsDest, meta.dest);
  fs.writeFileSync(destFull, frontmatter + cleanContent, 'utf-8');
}

// Generate Samples Catalog landing page: samples/index.md
const catalogContent = generateCatalogPage(sampleData);
fs.writeFileSync(path.join(docsDest, 'samples/index.md'), catalogContent, 'utf-8');

// Generate custom 404 page: 404.md
const notFoundContent = `---
title: "Page Not Found"
description: "The page you were looking for does not exist."
editUrl: false
---

The page you were looking for doesn't exist or has moved.

[Return to Documentation](/moonshine-go/)
`;
fs.writeFileSync(path.join(docsDest, '404.md'), notFoundContent, 'utf-8');

console.log(`[sync] Processed ${Object.keys(fileMap).length} pages, rewrote ${totalLinksRewritten} links.`);
if (errors > 0) {
  process.exit(1);
}

function parseSampleRating(content, sampleName) {
  // Extract top title
  let title = sampleName;
  const titleMatch = content.match(/^#\s+(.+)$/m);
  if (titleMatch) {
    title = titleMatch[1].replace(/—.*$/, '').replace(/—.*$/, '').trim();
  }

  // Extract first paragraph for description
  let description = '';
  const paraMatch = content.match(/(?:^#\s+.+\n\n)([\s\S]+?)(?=\n\n##|\n\n\|)/);
  if (paraMatch) {
    description = paraMatch[1].replace(/\n/g, ' ').trim();
  }

  // Extract rating table
  const rating = {
    id: sampleName,
    title,
    description,
    tier: 'Tier 1',
    complexity: '2/5',
    setupCost: 'Medium',
    pillars: 'Composability',
    industry: 'General',
    appeal: '4/5',
  };

  const tierMatch = content.match(/\|\s*\*\*Tier\*\*\s*\|\s*([^|]+)\s*\|/i);
  if (tierMatch) rating.tier = tierMatch[1].trim();

  const compMatch = content.match(/\|\s*\*\*Complexity\*\*\s*\|\s*([^|]+)\s*\|/i);
  if (compMatch) rating.complexity = compMatch[1].trim();

  const setupMatch = content.match(/\|\s*\*\*Setup Cost\*\*\s*\|\s*([^|]+)\s*\|/i);
  if (setupMatch) rating.setupCost = setupMatch[1].trim();

  const pillarsMatch = content.match(/\|\s*\*\*Pillars\*\*\s*\|\s*([^|]+)\s*\|/i);
  if (pillarsMatch) rating.pillars = pillarsMatch[1].trim();

  const indMatch = content.match(/\|\s*\*\*Industry \/ Use Case\*\*\s*\|\s*([^|]+)\s*\|/i);
  if (indMatch) rating.industry = indMatch[1].trim();

  const appMatch = content.match(/\|\s*\*\*Appeal\*\*\s*\|\s*([^|]+)\s*\|/i);
  if (appMatch) rating.appeal = appMatch[1].trim();

  return rating;
}

function generateCatalogPage(samples) {
  return `---
title: "Samples Catalog"
description: "Interactive directory of runnable samples across Tiers 0, 1, 2, and in-process embedding."
---

Every sample in \`samples/\` is an independent, runnable demonstration of the voice cascade, speech-to-text, or speech synthesis using \`moonshine serve\` or \`pkg/moonshine\`.

## Sample Matrix

| Sample | Tier | Complexity | Setup Cost | Pillars | Appeal |
|---|---|:---:|:---:|---|:---:|
${samples
  .map(
    (s) =>
      `| [**${s.id}**](/moonshine-go/samples/${s.id}/) | \`${s.tier}\` | ${s.complexity} | ${s.setupCost.split('(')[0].trim()} | ${s.pillars} | **${s.appeal}** |`
  )
  .join('\n')}

---

## All Samples

<div class="sample-grid">
${samples
  .map((s) => {
    let tierClass = 'tier-1';
    if (s.tier.includes('Tier 0')) tierClass = 'tier-0';
    else if (s.tier.includes('Tier 2')) tierClass = 'tier-2';
    else if (s.tier.includes('Native') || s.tier.includes('in-process')) tierClass = 'tier-native';

    return `  <div class="sample-card">
    <div>
      <span class="rating-chip ${tierClass}">${s.tier}</span>
      <span class="rating-chip">★ ${s.appeal}</span>
      <h3><a href="/moonshine-go/samples/${s.id}/">${s.id}</a></h3>
      <p>${s.description.slice(0, 160)}${s.description.length > 160 ? '...' : ''}</p>
    </div>
    <div class="sample-meta">
      <span>Complexity: ${s.complexity}</span>
      <span>${s.setupCost.split('(')[0].trim()}</span>
    </div>
  </div>`;
  })
  .join('\n')}
</div>
`;
}
