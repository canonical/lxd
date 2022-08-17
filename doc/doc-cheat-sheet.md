---
orphan: true
myst:
  substitutions:
    reuse_key: "This is **included** text."
    advanced_reuse_key: "This is a substitution that includes a code block:
                       ```
                       code block
                       ```"
---

# Documentation cheat sheet

The documentation files use a mixture of [Markdown](https://commonmark.org/) and [MyST](https://myst-parser.readthedocs.io/) syntax.

See the following sections for syntax help and conventions.

## Headings

```{list-table}
   :header-rows: 1

* - Input
  - Description
* - `# Title`
  - Page title and H1 heading
* - `## Heading`
  - H2 heading
* - `### Heading`
  - H3 heading
* - `#### Heading`
  - H4 heading
* - ...
  - Further headings
```

Adhere to the following conventions:

- Do not use consecutive headings without intervening text.
- Use sentence style for headings (capitalize only the first word).
- Do not skip levels (for example, always follow an H2 with an H3, not an H4).

## Inline formatting

```{list-table}
   :header-rows: 1

* - Input
  - Output
* - `*Italic*`
  - *Italic*
* - `**Bold**`
  - **Bold**
* - `` `code` ``
  - `code`

```

Adhere to the following conventions:

- Use italics sparingly. Common uses for italics are titles and names (for example, when referring to a section title that you cannot link to, or when introducing the name for a concept).
- Use bold sparingly. A common use for bold is UI elements ("Click **OK**"). Avoid using bold for emphasis and rather rewrite the sentence to get your point across.

## Code blocks

Start and end a code block with three back ticks: `` ``` ``

You can specify the code language after the back ticks to enforce a specific lexer, but in many cases, the default lexer works just fine.


```{list-table}
   :header-rows: 1

* - Input
  - Output
* - ````
    ```
    # Demonstrate a code block
    code:
    - example: true
    ```
    ````
  - ```
    # Demonstrate a code block
    code:
    - example: true
    ```
* - ````
    ```yaml
    # Demonstrate a code block
    code:
    - example: true
    ```
    ````
  - ```yaml
    # Demonstrate a code block
    code:
    - example: true
    ```

```

To include back ticks in a code block, increase the number of surrounding back ticks:

```{list-table}
   :header-rows: 1

* - Input
  - Output
* - `````
    ````
    ```
    ````
    `````
  - ````
    ```
    ````
```

## Links

How to link depends on if you are linking to an external URL or to another page in the documentation.

### External links

For external links, use only the URL, or Markdown syntax if you want to override the link text.

```{list-table}
   :header-rows: 1

* - Input
  - Output
* - `https://linuxcontainers.org`
  - [{spellexception}`https://linuxcontainers.org`](https://linuxcontainers.org)
* - `[Linux Containers](https://linuxcontainers.org)`
  - [Linux Containers](https://linuxcontainers.org)
```

To display a URL as text and prevent it from being linked, add a `<span></span>`:

```{list-table}
   :header-rows: 1

* - Input
  - Output
* - `https:/<span></span>/linuxcontainers.org`
  - {spellexception}`https:/<span></span>/linuxcontainers.org`

```

### Internal references

For internal references, both Markdown and MyST syntax are supported. In most cases, you should use MyST syntax though, because it resolves the link text automatically and gives an indication of the link in GitHub rendering.

#### Referencing a page

To reference a documentation page, use MyST syntax to automatically extract the link text. When overriding the link text, use Markdown syntax.

```{list-table}
   :header-rows: 1

* - Input
  - Output
  - Output on GitHub
  - Status
* - `` {doc}`index` ``
  - {doc}`index`
  - {doc}<span></span>`index`
  - Preferred.
* - `[](index)`
  - [](index)
  -
  - Do not use.
* - `[LXD documentation](index)`
  - [LXD documentation](index)
  - [LXD documentation](index)
  - Preferred when overriding the link text.
* - `` {doc}`LXD documentation <index>` ``
  - {doc}`LXD documentation <index>`
  - {doc}<span></span>`LXD documentation <index>`
  - Alternative when overriding the link text.

```
Adhere to the following conventions:
- Override the link text only when it is necessary. If you can use the document title as link text, do so, because the text will then update automatically if the title changes.
- Never "override" the link text with the same text that would be generated automatically.

(a_section_target)=
#### Referencing a section

To reference a section within the documentation (on the same page or on another page), you can either add a target to it and reference that target, or you can use an automatically generated anchor in combination with the file name.

Adhere to the following conventions:
- Add targets for sections that are central and a "typical" place to link to, so you expect they will be linked frequently. For "one-off" links, use the automatically generated anchors.
- Override the link text only when it is necessary. If you can use the section title as link text, do so, because the text will then update automatically if the title changes.
- Never "override" the link text with the same text that would be generated automatically.

##### Using a target

You can add targets at any place in the documentation. However, if there is no heading or title for the targeted element, you must specify a link text.

(a_random_target)=

```{list-table}
   :header-rows: 1

* - Input
  - Output
  - Output on GitHub
  - Description
* - `(target_ID)=`
  -
  - \(target_ID\)=
  - Adds the target ``target_ID``.
* - `` {ref}`a_section_target` ``
  - {ref}`a_section_target`
  - \{ref\}`a_section_target`
  - References a target that has a title.
* - `` {ref}`link text <a_random_target>` ``
  - {ref}`link text <a_random_target>`
  - \{ref\}`link text <a_random_target>`
  - References a target and specifies a title.
* - ``[`option name\](a_random_target)``
  - [`option name`](a_random_target)
  - [`option name`](https://) (link is broken)
  - Use Markdown syntax if you need markup on the link text.
```

##### Using an automatically generated anchor

When using MyST syntax, you must always specify the file name, even if the link points to a section in the same file.
When using Markdown syntax, you can leave out the file name when linking within the same file.

```{list-table}
   :header-rows: 1

* - Input
  - Output
  - Output on GitHub
  - Description
* - `` {ref}`doc-cheat-sheet.md#referencing-a-section` ``
  - {ref}`doc-cheat-sheet.md#referencing-a-section`
  - \{ref\}`doc-cheat-sheet.md#referencing-a-section`
  - References an automatically generated anchor.
* - `[](#referencing-a-section)`
  - [](#referencing-a-section)
  -
  - Do not use.
* - `[link text](#referencing-a-section)`
  - [link text](#referencing-a-section)
  - [link text](#referencing-a-section)
  - Preferred when overriding the link text.
* - `` {ref}`link text <doc-cheat-sheet.md#referencing-a-section>` ``
  - {ref}`link text <doc-cheat-sheet.md#referencing-a-section>`
  - \{ref\}`link text <doc-cheat-sheet.md#referencing-a-section>`
  - Alternative when overriding the link text.
```

## Navigation

Every documentation page must be included as a subpage to another page in the navigation.

This is achieved with the [`toctree`](https://www.sphinx-doc.org/en/master/usage/restructuredtext/directives.html#directive-toctree) directive in the parent page: <!-- wokeignore:rule=master -->

````
```{toctree}
:hidden:

subpage1
subpage2
```
````

If a page should not be included in the navigation, you can suppress the resulting build warning by putting the following instruction at the top of the file:

```
---
orphan: true
---
```

Use orphan pages sparingly and only if there is a clear reason for it.

## Lists

```{list-table}
   :header-rows: 1

* - Input
  - Output
* - ```
    - Item 1
    - Item 2
    - Item 3
    ```
  - - Item 1
    - Item 2
    - Item 3
* - ```
    1. Step 1
    1. Step 2
    1. Step 3
    ```
  - 1. Step 1
    1. Step 2
    1. Step 3
* - ```
    1. Step 1
       - Item 1
         * Subitem
       - Item 2
    1. Step 2
       1. Substep 1
       1. Substep 2
    ```
  - 1. Step 1
       - Item 1
         * Subitem
       - Item 2
    1. Step 2
       1. Substep 1
       1. Substep 2
```

Adhere to the following conventions:
- In numbered lists, use ``1.`` for all items to generate the step numbers automatically.
- Use `-` for unordered lists. When using nested lists, you can use `*` for the nested level.

### Definition lists

```{list-table}
   :header-rows: 1

* - Input
  - Output
* - ```
    Term 1
    : Definition

    Term 2
    : Definition
    ```
  - Term 1
    : Definition

    Term 2
    : Definition
```

## Tables

You can use standard Markdown tables. However, using the rST [list table](https://docutils.sourceforge.io/docs/ref/rst/directives.html#list-table) syntax is usually much easier.

Both markups result in the following output:

```{list-table}
   :header-rows: 1

* - Header 1
  - Header 2
* - Cell 1

    Second paragraph cell 1
  - Cell 2
* - Cell 3
  - Cell 4
```

### Markdown tables

```
| Header 1                           | Header 2 |
|------------------------------------|----------|
| Cell 1<br><br>2nd paragraph cell 1 | Cell 2   |
| Cell 3                             | Cell 4   |
```

### List tables

````
```{list-table}
   :header-rows: 1

* - Header 1
  - Header 2
* - Cell 1

    2nd paragraph cell 1
  - Cell 2
* - Cell 3
  - Cell 4
```
````

## Notes

```{list-table}
   :header-rows: 1

* - Input
  - Output
* - ````
    ```{note}
    A note.
    ```
    ````
  - ```{note}
    A note.
    ```
* - ````
    ```{tip}
    A tip.
    ```
    ````
  - ```{tip}
    A tip.
    ```
* - ````
    ```{important}
    Important information
    ```
    ````
  - ```{important}
    Important information.
    ```
* - ````
    ```{caution}
    This might damage your hardware!
    ```
    ````
  - ```{caution}
    This might damage your hardware!
    ```


```

Adhere to the following conventions:
- Use notes sparingly.
- Only use the following note types: `note`, `tip`, `important`, `caution`
- Only use a caution if there is a clear hazard of hardware damage or data loss.

## Images

```{list-table}
   :header-rows: 1

* - Input
  - Output
* - ```
    ![Alt text](https://linuxcontainers.org/static/img/containers.png)
    ```
  - ![Alt text](https://linuxcontainers.org/static/img/containers.png)
* - ````
    ```{figure} https://linuxcontainers.org/static/img/containers.png
       :width: 100px
       :alt: Alt text

       Figure caption
    ```
    ````
  - ```{figure} https://linuxcontainers.org/static/img/containers.png
       :width: 100px
       :alt: Alt text

       Figure caption
    ```
```

Adhere to the following conventions:
- For pictures in the `doc` folder, start the path with `/` (for example, `/images/image.png`).
- Use PNG format for screenshots and SVG format for graphics.

## Reuse

A big advantage of MyST in comparison to plain Markdown is that it allows to reuse content.

### Substitution

To reuse sentences or paragraphs without too much markup and special formatting, use substitutions.

Substitutions can be defined in the following locations:
- In the `substitutions.yaml` file. Substitutions defined in this file are available in all documentation pages.
- At the top of a single file in the following format:

  ````
  ---
  myst:
    substitutions:
      reuse_key: "This is **included** text."
      advanced_reuse_key: "This is a substitution that includes a code block:
                         ```
                         code block
                         ```"

  ---
  ````

You can combine both options by defining a default substitution in `reuse/substitutions.py` and overriding it at the top of a file.

```{list-table}
   :header-rows: 1

* - Input
  - Output
* - `{{reuse_key}}`
  - {{reuse_key}}
* - `{{advanced_reuse_key}}`
  - {{advanced_reuse_key}}
```

Adhere to the following convention:
- Substitutions do not work on GitHub. Therefore, use key names that indicate the included text (for example, `note_not_supported` instead of `reuse_note`).

### File inclusion

To reuse longer sections or text with more advanced markup, you can put the content in a separate file and include the file or parts of the file in several locations.

You cannot put any targets into the content that is being reused (because references to this target would be ambiguous then). You can, however, put a target right before including the file.

By combining file inclusion and substitutions, you can even replace parts of the included text.

`````{list-table}
   :header-rows: 1

* - Input
  - Output
* - ````
    % Include parts of the content from file [../README.md](../README.md)
    ```{include} ../README.md
       :start-after: Installing LXD from packages
       :end-before: <!-- Include end installing -->
    ```
    ````
  - % Include parts of the content from file [../README.md](../README.md)
    ```{include} ../README.md
       :start-after: Installing LXD from packages
       :end-before: <!-- Include end installing -->
    ```
`````

Adhere to the following convention:
- File inclusion does not work on GitHub. Therefore, always add a comment linking to the included file.
- To select parts of the text, add HTML comments for the start and end points and use `:start-after:` and `:end-before:`, if possible. You can combine `:start-after:` and `:end-before:` with `:start-line:` and `:end-line:` if required. Using only `:start-line:` and `:end-line:` is error-prone though.

## Tabs


``````{list-table}
   :header-rows: 1

* - Input
  - Output
* - `````
    ````{tabs}

    ```{group-tab} Tab 1

    Content Tab 1
    ```
    ```{group-tab} Tab 2

    Content Tab 2
    ```
    ````
    `````
  - ````{tabs}

    ```{group-tab} Tab 1

    Content Tab 1
    ```
    ```{group-tab} Tab 2

    Content Tab 2
    ```
    ````
``````

## Collapsible sections

There is no support for details sections in rST, but you can insert HTML to create them.

```{list-table}
   :header-rows: 1

* - Input
  - Output
* - ```
    <details>
    <summary>Details</summary>

    Content
    </details>
    ```
  - <details>
    <summary>Details</summary>

    Content
    </details>
```

## Glossary

You can define glossary terms in any file. Ideally, all terms should be collected in one glossary file though, and they can then be referenced from any file.

`````{list-table}
   :header-rows: 1

* - Input
  - Output
* - ````
    ```{glossary}

    example term
      Definition of the example term.
    ```
    ````
  - ```{glossary}

    example term
      Definition of the example term.
    ```
* - ``{term}`example term` ``
  - {term}`example term`
`````
