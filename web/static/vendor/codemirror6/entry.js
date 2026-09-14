// Inputs for the Code tab's CodeMirror 6 bundle (window.CM6). Everything
// the pane needs — core API, commands, search, autocomplete, the language
// packages and their parsers — is re-exported here so
// web/static/js/editor.js never needs an import map.
import * as state from "@codemirror/state";
import * as view from "@codemirror/view";
import * as commands from "@codemirror/commands";
import * as language from "@codemirror/language";
import * as autocomplete from "@codemirror/autocomplete";
import * as search from "@codemirror/search";
import { basicSetup } from "codemirror";
import { languages } from "@codemirror/language-data";
import { tags } from "@lezer/highlight";

export const CM6 = {
  EditorState: state.EditorState,
  StateEffect: state.StateEffect,
  StateField: state.StateField,
  Compartment: state.Compartment,
  EditorView: view.EditorView,
  keymap: view.keymap,
  drawSelection: view.drawSelection,
  dropCursor: view.dropCursor,
  rectangularSelection: view.rectangularSelection,
  crosshairCursor: view.crosshairCursor,
  highlightActiveLine: view.highlightActiveLine,
  lineNumbers: view.lineNumbers,
  foldGutter: view.foldGutter,
  indentUnit: view.indentUnit,
  HighlightStyle: language.HighlightStyle,
  syntaxHighlighting: language.syntaxHighlighting,
  indentOnInput: language.indentOnInput,
  bracketMatching: language.bracketMatching,
  foldKeymap: language.foldKeymap,
  autocompletion: autocomplete.autocompletion,
  closeBrackets: autocomplete.closeBrackets,
  closeBracketsKeymap: autocomplete.closeBracketsKeymap,
  completionKeymap: autocomplete.completionKeymap,
  searchKeymap: search.searchKeymap,
  highlightSelectionMatches: search.highlightSelectionMatches,
  openSearchPanel: search.openSearchPanel,
  closeSearchPanel: search.closeSearchPanel,
  setSearchQuery: search.setSearchQuery,
  getSearchQuery: search.getSearchQuery,
  SearchQuery: search.SearchQuery,
  history: commands.history,
  historyKeymap: commands.historyKeymap,
  defaultKeymap: commands.defaultKeymap,
  indentWithTab: commands.indentWithTab,
  lineComment: commands.lineComment,
  toggleComment: commands.toggleComment,
  basicSetup: basicSetup,
  languages: languages,
  LanguageDescription: language.LanguageDescription,
  tags: tags,
};

if (typeof window !== "undefined") window.CM6 = CM6;

export default CM6;