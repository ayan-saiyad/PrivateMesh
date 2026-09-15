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

      <section className="status-panel" aria-labelledby="ownership-heading">
        <div>
          <p className="status-label">Private by design</p>
          <h2 id="ownership-heading">Your documents stay where they live</h2>
          <p>
            Each search node owns its content and index. The coordinator only
            sends queries and combines the results.
          </p>
        </div>
        <span className="status-indicator">Local-first</span>
      </section>
    </main>
  );
}
