/*
 * search_scorer.js
 *
 * Custom Sphinx search scorer that adjusts the ranking of search results.
 * Loaded on the search page via the extrahead block in _templates/base.html.
 *
 * The base configuration this script adapts is defined in:
 *   https://github.com/sphinx-doc/sphinx/blob/master/sphinx/themes/basic/static/searchtools.js
 * See also:
 *   https://www.sphinx-doc.org/en/master/usage/configuration.html#confval-html_search_scorer
 *
 * Note: this script is injected via base.html rather than the html_search_scorer
 * config option in conf.py, because the canonical_sphinx theme overrides the
 * Sphinx search templates that include that hook.
 */

// Read the search query once so we can boost pages whose H1 title contains it.
const _searchQuery = new URLSearchParams(window.location.search).get("q")?.toLowerCase().trim() || "";

window.Scorer = {
  score: result => {
    const [docName, title, anchor, descr, baseScore, filename] = result;
    let score = baseScore;

    // Demote low-value reference pages that often match queries but are
    // rarely what the user is looking for.
    if (docName.startsWith("reference/manpages/") ||
        docName.startsWith("reference/release-notes/release-notes-") ||
        docName.split("/").pop() === "api-extensions") {
      return score - 20;
    }

    // Sphinx scores all pages with the query in any heading equally.
    // Give an extra boost when the query also appears in the page's H1 title,
    // so pages primarily about the topic rank above pages that merely mention it in a sub-heading.
    if (anchor === "" && _searchQuery && title.toLowerCase().includes(_searchQuery)) score += 5;

    return score;
  },

  // Query matches the full name of an object.
  objNameMatch: 11,
  // Query matches in the last dotted part of the object name.
  objPartialMatch: 6,
  // Additive scores depending on the priority of the object.
  objPrio: {
    0: 15, // used to be importantResults
    1: 5,  // used to be objectResults
    2: -5, // used to be unimportantResults
  },
  // Used when the priority is not in the mapping.
  objPrioDefault: 0,

  // Query found in title.
  title: 15,
  partialTitle: 7,

  // Query found in terms.
  term: 5,
  partialTerm: 2,
};
