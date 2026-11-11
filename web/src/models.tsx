import { useEffect, useState } from "react";
import { api } from "./api";

type Strategy = {
  name: string;
  description: string;
  aliases: {
    alias: string;
    models: {
      model: string;
      available: boolean;
      reason?: string;
      retry_at?: string;
    }[];
  }[];
};
type Catalog = {
  default_model: string;
  strategies: {
    default_strategy: string;
    default_reviewer_model?: string;
    strategies: Strategy[];
  };
};

// This is a view of the runner's choices. Dot selects them per job through MCP.
export function Models() {
  const [catalog, setCatalog] = useState<Catalog | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    document.title = "dotshift · Models";
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function refresh() {
      try {
        const next = await api<Catalog>("/models", { signal: controller.signal });
        if (!controller.signal.aborted) {
          setCatalog(next);
          setError("");
        }
      } catch (e) {
        if (!controller.signal.aborted)
          setError(e instanceof Error ? e.message : "Could not load models.");
      }
      if (!controller.signal.aborted)
        timer = setTimeout(refresh, document.hidden ? 30000 : 10000);
    }
    void refresh();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, []);
  const choices = catalog?.strategies;
  const aliases = choices?.strategies.find((strategy) => strategy.name === "alias")?.aliases || [];
  return (
    <div className="page models-page">
      <div className="page-heading">
        <div>
          <h1>Model choices</h1>
          <p>Ask Dot to choose a configured alias, model policy, and reviewer for each job.</p>
        </div>
      </div>
      {error && <div className="notice warning" role="alert">{error}</div>}
      {!catalog ? (
        <p>{error ? "Waiting for the local service." : "Loading model choices…"}</p>
      ) : (
        <>
          <section className="panel model-defaults" aria-label="Model defaults">
            <h2>Defaults</h2>
            <dl>
              <dt>Model alias</dt><dd><code>{catalog.default_model}</code></dd>
              <dt>Policy</dt><dd><code>{choices!.default_strategy}</code></dd>
              <dt>Separate review</dt><dd>{choices!.default_reviewer_model || "Fresh session with the coding model"}</dd>
            </dl>
            <p>Change these defaults or add model aliases in <code>dotshift.yaml</code>, then restart the service.</p>
          </section>
          <section aria-label="Configured aliases">
            <h2>Configured aliases</h2>
            <div className="model-aliases">
              {aliases.map((alias) => (
                <section className="panel model-alias" key={alias.alias}>
                  <h3><code>{alias.alias}</code>{alias.alias === catalog.default_model && <span>Default</span>}</h3>
                  <ol>
                    {alias.models.map((model) => (
                      <li key={model.model}>
                        <code>{model.model}</code>
                        <small className={model.available ? "" : "model-unavailable"}>
                          {model.available ? "No known limit" : model.reason || "Unavailable"}
                          {!model.available && model.retry_at && ` · Retry ${new Date(model.retry_at).toLocaleString()}`}
                        </small>
                      </li>
                    ))}
                  </ol>
                </section>
              ))}
            </div>
            <p className="panel-description">Availability reflects limits seen during jobs. It does not measure remaining tokens or check sign-in.</p>
          </section>
          <section className="panel model-policies" aria-label="Model policies">
            <h2>Model policies</h2>
            {choices!.strategies.map((strategy) => (
              <div key={strategy.name}>
                <h3><code>{strategy.name}</code></h3>
                <p>{strategy.description}</p>
                <small>Eligible aliases: {strategy.aliases.map((alias) => alias.alias).join(", ") || "None configured"}</small>
              </div>
            ))}
            <p>With automatic review, an available model performs a fresh, read-only review. In subscription mode, Claude can finish both coding and review when OpenAI is unavailable.</p>
          </section>
        </>
      )}
    </div>
  );
}
