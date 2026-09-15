import { type FormEvent, useEffect, useState } from "react";

type SearchResult = {
  document_id: string;
  collection_id: string;
  node_id: string;
  title: string;
  snippet: string;
  score: number;
  rank: number;
};

type SearchResponse = {
  request_id: string;
  mode: string;
  results: SearchResult[];
  searched_shard_ids: string[];
  unavailable_shard_ids: string[];
};

type NodeSummary = {
  id: string;
  collection_ids: string[];
  applied_log_offset: number;
};

type DocumentResponse = {
  document_id: string;
  media_type: string;
  content: string;
};

async function responseJSON<T>(response: Response): Promise<T> {
  const body = (await response.json()) as T & { error?: string };
  if (!response.ok) {
    throw new Error(
      body.error ?? `Request failed with status ${response.status}`,
    );
  }
  return body;
}

export function App() {
  const [query, setQuery] = useState("");
  const [mode, setMode] = useState("auto");
  const [nodes, setNodes] = useState<NodeSummary[]>([]);
  const [search, setSearch] = useState<SearchResponse | null>(null);
  const [selected, setSelected] = useState<DocumentResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let active = true;
    fetch("/api/nodes")
      .then((response) => responseJSON<{ nodes: NodeSummary[] }>(response))
      .then((body) => {
        if (active) setNodes(body.nodes);
      })
      .catch(() => {
        if (active) setNodes([]);
      });
    return () => {
      active = false;
    };
  }, []);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!query.trim()) return;
    setLoading(true);
    setError("");
    setSelected(null);
    try {
      const response = await fetch("/api/search", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ query, mode, limit: 12, timeout_ms: 1500 }),
      });
      setSearch(await responseJSON<SearchResponse>(response));
    } catch (requestError) {
      setSearch(null);
      setError(
        requestError instanceof Error ? requestError.message : "Search failed",
      );
    } finally {
      setLoading(false);
    }
  }

  async function openDocument(result: SearchResult) {
    setError("");
    try {
      const response = await fetch(
        `/api/documents/${encodeURIComponent(result.collection_id)}/${encodeURIComponent(result.document_id)}`,
      );
      setSelected(await responseJSON<DocumentResponse>(response));
    } catch (requestError) {
      setError(
        requestError instanceof Error
          ? requestError.message
          : "Document retrieval failed",
      );
    }
  }

  const collections = new Set(nodes.flatMap((node) => node.collection_ids));

  return (
    <main className="shell">
      <header className="masthead">
        <a className="brand" href="/" aria-label="PrivateMesh home">
          <span className="product-mark" aria-hidden="true">
            PM
          </span>
          <span>PrivateMesh</span>
        </a>
        <div className="mesh-status" aria-live="polite">
          <span
            className={nodes.length > 0 ? "status-dot online" : "status-dot"}
          />
          {nodes.length} {nodes.length === 1 ? "node" : "nodes"} ·{" "}
          {collections.size}{" "}
          {collections.size === 1 ? "collection" : "collections"}
        </div>
      </header>

      <section className="hero" aria-labelledby="search-heading">
        <p className="eyebrow">Search without centralizing</p>
        <h1 id="search-heading">
          Find it everywhere. Keep it where it belongs.
        </h1>
        <p className="hero-copy">
          One query crosses independently owned nodes. Documents are filtered by
          policy where they live, and only permitted results return.
        </p>

        <form className="search-form" onSubmit={submit}>
          <label className="query-field">
            <span className="sr-only">Search documents</span>
            <svg viewBox="0 0 24 24" aria-hidden="true">
              <circle cx="11" cy="11" r="6.5" />
              <path d="m16 16 4 4" />
            </svg>
            <input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search across the mesh"
              autoComplete="off"
            />
          </label>
          <label className="mode-field">
            <span className="sr-only">Retrieval mode</span>
            <select
              value={mode}
              onChange={(event) => setMode(event.target.value)}
            >
              <option value="auto">Adaptive</option>
              <option value="lexical">Lexical</option>
              <option value="vector">Vector</option>
              <option value="hybrid">Hybrid</option>
            </select>
          </label>
          <button type="submit" disabled={loading || !query.trim()}>
            {loading ? "Searching…" : "Search"}
          </button>
        </form>
        <p className="search-hint">
          Try “replica recovery,” “distributed search,” or “local ownership.”
        </p>
      </section>

      {error && <p className="error-banner">{error}</p>}

      {search && (
        <section
          className="results"
          aria-live="polite"
          aria-label="Search results"
        >
          <div className="results-heading">
            <div>
              <p className="eyebrow">{search.mode} retrieval</p>
              <h2>
                {search.results.length}{" "}
                {search.results.length === 1 ? "result" : "results"}
              </h2>
            </div>
            <p>
              {search.searched_shard_ids.length} shards searched
              {search.unavailable_shard_ids.length > 0 &&
                ` · ${search.unavailable_shard_ids.length} unavailable`}
            </p>
          </div>

          <div className="result-list">
            {search.results.map((result) => (
              <button
                className="result-card"
                key={`${result.collection_id}:${result.document_id}`}
                onClick={() => openDocument(result)}
                type="button"
              >
                <span className="rank">
                  {String(result.rank).padStart(2, "0")}
                </span>
                <span className="result-copy">
                  <span className="result-meta">
                    {result.collection_id} <i /> {result.node_id}
                  </span>
                  <strong>{result.title || result.document_id}</strong>
                  <span>{result.snippet}</span>
                </span>
                <span className="open-arrow" aria-hidden="true">
                  ↗
                </span>
              </button>
            ))}
            {search.results.length === 0 && (
              <div className="empty-state">
                <p>No permitted documents matched this query.</p>
                <span>Try a broader term or another retrieval mode.</span>
              </div>
            )}
          </div>
        </section>
      )}

      <section className="ownership-strip" aria-label="Privacy model">
        <div>
          <span>01</span>
          <strong>Query routed</strong>
          <p>The coordinator selects active owners.</p>
        </div>
        <div>
          <span>02</span>
          <strong>Policy checked locally</strong>
          <p>Each node decides what can leave.</p>
        </div>
        <div>
          <span>03</span>
          <strong>Ranks merged</strong>
          <p>Only authorized snippets are combined.</p>
        </div>
      </section>

      {selected && (
        <div className="document-backdrop">
          <article
            className="document-panel"
            role="dialog"
            aria-modal="true"
            aria-labelledby="document-title"
          >
            <button
              className="close-button"
              type="button"
              onClick={() => setSelected(null)}
            >
              Close
            </button>
            <p className="eyebrow">Authorized document</p>
            <h2 id="document-title">{selected.document_id}</h2>
            <p className="media-type">{selected.media_type}</p>
            <div className="document-content">{selected.content}</div>
          </article>
        </div>
      )}
    </main>
  );
}
