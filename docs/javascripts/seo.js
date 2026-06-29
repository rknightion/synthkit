/* SEO enhancements for synthkit documentation */

document.addEventListener('DOMContentLoaded', function() {
  addStructuredData();
  enhanceMetaTags();
  addOpenGraphTags();
  addTwitterCardTags();
  addCanonicalURL();
});

// Add JSON-LD structured data
function addStructuredData() {
  const structuredData = {
    "@context": "https://schema.org",
    "@type": "SoftwareApplication",
    "name": "synthkit",
    "applicationCategory": "Observability / Monitoring Software",
    "operatingSystem": "Docker / Linux / macOS",
    "description": "Composable synthetic-telemetry generator for Grafana Cloud — declare infrastructure and applications in one YAML blueprint and emit structurally-correct synthetic metrics, traces, logs, and RUM.",
    "url": "https://m7kni.io/synthkit/",
    "downloadUrl": "https://github.com/rknightion/synthkit",
    "softwareVersion": "latest",
    "programmingLanguage": [
      "Go"
    ],
    "license": "https://github.com/rknightion/synthkit/blob/main/LICENSE",
    "author": {
      "@type": "Person",
      "name": "Rob Knighton",
      "url": "https://github.com/rknightion"
    },
    "maintainer": {
      "@type": "Person",
      "name": "Rob Knighton",
      "url": "https://github.com/rknightion"
    },
    "codeRepository": "https://github.com/rknightion/synthkit",
    "runtimePlatform": [
      "Go",
      "Docker"
    ],
    "applicationSubCategory": [
      "Synthetic Telemetry Generator",
      "Observability Demo Data",
      "Grafana Cloud Tooling"
    ],
    "offers": {
      "@type": "Offer",
      "price": "0",
      "priceCurrency": "USD"
    },
    "screenshot": "https://m7kni.io/synthkit/assets/social-card.png",
    "featureList": [
      "YAML blueprint declaration of infrastructure and applications",
      "Technology-native metric, label, and field names",
      "Prometheus Remote-Write v2 metrics to Grafana Cloud",
      "OTLP traces with end-to-end request correlation",
      "Loki logs and optional Faro/RUM beacons",
      "Kubernetes, CloudWatch, CSP, database, and AI/LLM signal families",
      "Incident scenarios and a live control plane",
      "Docker single-binary deployment"
    ]
  };

  const docData = {
    "@context": "https://schema.org",
    "@type": "TechArticle",
    "headline": document.title,
    "description": document.querySelector('meta[name="description"]')?.content || "synthkit documentation",
    "url": window.location.href,
    "datePublished": document.querySelector('meta[name="date"]')?.content,
    "dateModified": document.querySelector('meta[name="git-revision-date-localized"]')?.content,
    "author": {
      "@type": "Person",
      "name": "Rob Knighton"
    },
    "publisher": {
      "@type": "Organization",
      "name": "synthkit",
      "url": "https://m7kni.io/synthkit/"
    },
    "mainEntityOfPage": {
      "@type": "WebPage",
      "@id": window.location.href
    },
    "articleSection": getDocumentationSection(),
    "keywords": getPageKeywords(),
    "about": {
      "@type": "SoftwareApplication",
      "name": "synthkit"
    }
  };

  const script1 = document.createElement('script');
  script1.type = 'application/ld+json';
  script1.textContent = JSON.stringify(structuredData);
  document.head.appendChild(script1);

  const script2 = document.createElement('script');
  script2.type = 'application/ld+json';
  script2.textContent = JSON.stringify(docData);
  document.head.appendChild(script2);
}

// Enhance existing meta tags
function enhanceMetaTags() {
  if (!document.querySelector('meta[name="robots"]')) {
    addMetaTag('name', 'robots', 'index, follow, max-snippet:-1, max-image-preview:large, max-video-preview:-1');
  }

  addMetaTag('name', 'language', 'en');
  addMetaTag('http-equiv', 'Content-Type', 'text/html; charset=utf-8');

  if (!document.querySelector('meta[name="viewport"]')) {
    addMetaTag('name', 'viewport', 'width=device-width, initial-scale=1');
  }

  const keywords = getPageKeywords();
  if (keywords) {
    addMetaTag('name', 'keywords', keywords);
  }

  if (isDocumentationPage()) {
    addMetaTag('name', 'article:tag', 'synthetic-telemetry');
    addMetaTag('name', 'article:tag', 'observability');
    addMetaTag('name', 'article:tag', 'grafana-cloud');
    addMetaTag('name', 'article:tag', 'prometheus');
  }
}

// Add Open Graph tags
function addOpenGraphTags() {
  const title = document.title || 'synthkit';
  const description = document.querySelector('meta[name="description"]')?.content ||
    'Composable synthetic-telemetry generator for Grafana Cloud — declare infrastructure and applications in one YAML blueprint.';
  const url = window.location.href;
  const siteName = 'synthkit Documentation';

  addMetaTag('property', 'og:type', 'website');
  addMetaTag('property', 'og:site_name', siteName);
  addMetaTag('property', 'og:title', title);
  addMetaTag('property', 'og:description', description);
  addMetaTag('property', 'og:url', url);
  addMetaTag('property', 'og:locale', 'en_US');
  addMetaTag('property', 'og:image', 'https://m7kni.io/synthkit/assets/social-card.png');
  addMetaTag('property', 'og:image:width', '1200');
  addMetaTag('property', 'og:image:height', '630');
  addMetaTag('property', 'og:image:alt', 'synthkit - synthetic-telemetry generator for Grafana Cloud');
}

// Add Twitter Card tags
function addTwitterCardTags() {
  const title = document.title || 'synthkit';
  const description = document.querySelector('meta[name="description"]')?.content ||
    'Composable synthetic-telemetry generator for Grafana Cloud — declare infrastructure and applications in one YAML blueprint.';

  addMetaTag('name', 'twitter:card', 'summary_large_image');
  addMetaTag('name', 'twitter:title', title);
  addMetaTag('name', 'twitter:description', description);
  addMetaTag('name', 'twitter:image', 'https://m7kni.io/synthkit/assets/social-card.png');
  addMetaTag('name', 'twitter:creator', '@rknightion');
  addMetaTag('name', 'twitter:site', '@rknightion');
}

// Add canonical URL
function addCanonicalURL() {
  if (!document.querySelector('link[rel="canonical"]')) {
    const canonical = document.createElement('link');
    canonical.rel = 'canonical';
    canonical.href = window.location.href;
    document.head.appendChild(canonical);
  }
}

// Helper functions
function addMetaTag(attribute, name, content) {
  if (!document.querySelector(`meta[${attribute}="${name}"]`)) {
    const meta = document.createElement('meta');
    meta.setAttribute(attribute, name);
    meta.content = content;
    document.head.appendChild(meta);
  }
}

function getDocumentationSection() {
  const path = window.location.pathname;
  if (path.includes('/signals/') || path.includes('/signal-areas/')) return 'Signal Catalogue';
  if (path.includes('/blueprint')) return 'Writing Blueprints';
  if (path.includes('/architecture/')) return 'Architecture';
  if (path.includes('/configuration/')) return 'Configuration';
  if (path.includes('/installation/')) return 'Installation';
  if (path.includes('/getting-started/')) return 'Getting Started';
  if (path.includes('/quickstart/')) return 'Quick Start';
  if (path.includes('/deployment/') || path.includes('/RUNBOOK/')) return 'Deployment';
  if (path.includes('/control-plane/')) return 'Control Plane';
  if (path.includes('/troubleshooting/')) return 'Troubleshooting';
  if (path.includes('/cli/') || path.includes('/constructs/') || path.includes('/tools/')) return 'Reference';
  return 'Documentation';
}

function getPageKeywords() {
  const path = window.location.pathname;

  let keywords = ['synthetic telemetry', 'observability', 'grafana cloud', 'prometheus', 'opentelemetry', 'metrics'];

  if (path.includes('/signals/') || path.includes('/signal-areas/')) keywords.push('signal-catalogue', 'metric-names', 'labels');
  if (path.includes('/blueprint')) keywords.push('yaml', 'blueprint', 'declaration', 'workloads');
  if (path.includes('/architecture/')) keywords.push('go', 'constructs', 'composition-root', 'sinks');
  if (path.includes('/configuration/')) keywords.push('environment-variables', 'env', 'docker');
  if (path.includes('/installation/')) keywords.push('installation', 'docker-compose', 'setup');
  if (path.includes('/getting-started/')) keywords.push('tutorial', 'quick-start', 'guide');
  if (path.includes('/deployment/') || path.includes('/RUNBOOK/')) keywords.push('deployment', 'runbook', 'docker');
  if (path.includes('/control-plane/')) keywords.push('control-plane', 'operator-ui', 'scaling', 'incidents');
  if (path.includes('/troubleshooting/')) keywords.push('troubleshooting', 'debugging', 'errors');

  return keywords.join(', ');
}

function isDocumentationPage() {
  return !window.location.pathname.endsWith('/') ||
         window.location.pathname.includes('/docs/');
}
