import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { App } from "./App";

describe("App", () => {
  it("describes local document ownership", () => {
    render(<App />);

    expect(
      screen.getByRole("heading", { name: /distributed search/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: /documents stay where they live/i }),
    ).toBeInTheDocument();
    expect(screen.getByText("Local-first")).toBeInTheDocument();
  });
});
