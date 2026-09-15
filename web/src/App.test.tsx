import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { App } from "./App";

afterEach(() => {
  vi.restoreAllMocks();
});

describe("App", () => {
  it("searches the mesh and opens an authorized document", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(async (input) => {
        const url = String(input);
        if (url === "/api/nodes") {
          return Response.json({
            nodes: [
              {
                id: "node-a",
                collection_ids: ["engineering"],
                applied_log_offset: 4,
              },
            ],
          });
        }
        if (url === "/api/search") {
          return Response.json({
            request_id: "request-1",
            mode: "hybrid",
            results: [
              {
                document_id: "recovery-runbook",
                collection_id: "engineering",
                node_id: "node-a",
                title: "Replica recovery runbook",
                snippet: "Recover from committed log entries.",
                score: 0.9,
                rank: 1,
              },
            ],
            searched_shard_ids: ["node-a:engineering"],
            unavailable_shard_ids: [],
          });
        }
        return Response.json({
          document_id: "recovery-runbook",
          media_type: "text/plain; charset=utf-8",
          content: "Full recovery instructions.",
        });
      });

    render(<App />);
    expect(
      screen.getByRole("heading", { name: /find it everywhere/i }),
    ).toBeInTheDocument();
    await screen.findByText(/1 node · 1 collection/i);

    fireEvent.change(screen.getByPlaceholderText(/search across the mesh/i), {
      target: { value: "replica recovery" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Search" }));
    expect(
      await screen.findByText("Replica recovery runbook"),
    ).toBeInTheDocument();
    expect(screen.getByText(/1 shards searched/i)).toBeInTheDocument();

    fireEvent.click(
      screen.getByRole("button", { name: /replica recovery runbook/i }),
    );
    expect(
      await screen.findByText("Full recovery instructions."),
    ).toBeInTheDocument();
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
  });
});
