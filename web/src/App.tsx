export function App() {
  return (
    <main className="shell">
      <header className="masthead">
        <span className="product-mark" aria-hidden="true">
          PM
        </span>
        <div>
          <p className="eyebrow">PrivateMesh</p>
          <h1>Distributed search, locally owned.</h1>
        </div>
      </header>

      <section className="status-panel" aria-labelledby="foundation-heading">
        <div>
          <p className="status-label">Foundation</p>
          <h2 id="foundation-heading">Service environment ready</h2>
          <p>
            Indexing and query execution will be added in measured,
            independently testable phases.
          </p>
        </div>
        <span className="status-indicator">Phase 0</span>
      </section>
    </main>
  );
}
