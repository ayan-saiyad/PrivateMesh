import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { App } from "./App";

describe("App", () => {
  it("identifies the foundation phase", () => {
    render(<App />);

    expect(
      screen.getByRole("heading", { name: /distributed search/i }),
    ).toBeInTheDocument();
    expect(screen.getByText("Phase 0")).toBeInTheDocument();
  });
});
