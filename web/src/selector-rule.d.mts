import type selectorParser from "postcss-selector-parser";

export type SelectorTerm =
  | { type: "tag" | "universal" | "class" | "id"; value: string }
  | {
      type: "attribute";
      name: string;
      operator?: string;
      value?: string;
      flag?: "i" | "s";
    }
  | {
      type: "logic";
      logic: "any" | "none" | "relative";
      alternatives: SelectorChain[];
    }
  | { type: "pseudo-element"; name: string; raw: string }
  | {
      type: "pseudo-class";
      name: string;
      raw: string;
      functional: boolean;
      hasFilter?: boolean;
    }
  | { type: "raw"; raw: string };

export type SelectorChain = {
  steps: { relation: string | null; terms: SelectorTerm[] }[];
  relative: boolean;
  raw: string;
};

export type SelectorDescription = {
  text: string;
  children?: SelectorDescription[];
};

export function parseSelector(source: string): selectorParser.Root;
export function selectorSemantics(root: selectorParser.Root): SelectorChain[];
export function describeSelectorChinese(
  alternatives: SelectorChain[],
): SelectorDescription;

export function analyzeSelector(
  source: string,
  validateMatch?: (source: string) => void,
): {
  description: SelectorDescription | null;
  error: string | null;
  warnings: string[];
};
