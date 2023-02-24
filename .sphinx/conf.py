import datetime
import os
import sys
import yaml

# Project config.
project = "LXD"
author = "LXD contributors"
copyright = "2014-%s %s" % (datetime.date.today().year, author)

with open("../shared/version/flex.go") as fd:
    version = fd.read().split("\n")[-2].split()[-1].strip("\"")


# Extensions.
extensions = [
    "myst_parser",
    "sphinx_tabs.tabs",
    "sphinx_reredirects",
    "sphinxext.opengraph",
    "youtube-links",
    "related-links",
    "custom-rst-roles",
    "sphinxcontrib.jquery",
    "sphinx_copybutton",
    "sphinx.ext.intersphinx",
    "terminal-output",
    "config-options"
]

myst_enable_extensions = [
    "substitution",
    "deflist",
    "linkify"
]

myst_linkify_fuzzy_links=False
myst_heading_anchors = 7

if os.path.exists("../doc/substitutions.yaml"):
    with open("../doc/substitutions.yaml", "r") as fd:
        myst_substitutions = yaml.safe_load(fd.read())

intersphinx_mapping = {
    'cloud-init': ('https://cloudinit.readthedocs.io/en/latest/', None)
}

# Setup theme.
templates_path = ["_templates"]

html_theme = "furo"
html_show_sphinx = False
html_last_updated_fmt = ""
html_favicon = "_static/download/favicon.ico"
html_static_path = ['_static']
html_css_files = ['custom.css']
html_js_files = ['header-nav.js','version-switcher.js']
html_extra_path = ['_extra']

html_theme_options = {
    "sidebar_hide_name": True,
    "light_css_variables": {
        "font-stack": "Ubuntu, -apple-system, Segoe UI, Roboto, Oxygen, Cantarell, Fira Sans, Droid Sans, Helvetica Neue, sans-serif",
        "font-stack--monospace": "Ubuntu Mono, Consolas, Monaco, Courier, monospace",
        "color-foreground-primary": "#111",
        "color-foreground-secondary": "var(--color-foreground-primary)",
        "color-foreground-muted": "#333",
        "color-background-secondary": "#FFF",
        "color-background-hover": "#f2f2f2",
        "color-brand-primary": "#111",
        "color-brand-content": "#06C",
        "color-api-background": "#cdcdcd",
        "color-inline-code-background": "rgba(0,0,0,.03)",
        "color-sidebar-link-text": "#111",
        "color-sidebar-item-background--current": "#ebebeb",
        "color-sidebar-item-background--hover": "#f2f2f2",
        "toc-font-size": "var(--font-size--small)",
        "color-admonition-title-background--note": "var(--color-background-primary)",
        "color-admonition-title-background--tip": "var(--color-background-primary)",
        "color-admonition-title-background--important": "var(--color-background-primary)",
        "color-admonition-title-background--caution": "var(--color-background-primary)",
        "color-admonition-title--note": "#24598F",
        "color-admonition-title--tip": "#24598F",
        "color-admonition-title--important": "#C7162B",
        "color-admonition-title--caution": "#F99B11",
        "color-highlighted-background": "#EbEbEb",
        "color-link-underline": "var(--color-background-primary)",
        "color-link-underline--hover": "var(--color-background-primary)",
    },
    "dark_css_variables": {
        "color-foreground-secondary": "var(--color-foreground-primary)",
        "color-foreground-muted": "#CDCDCD",
        "color-background-secondary": "var(--color-background-primary)",
        "color-background-hover": "#666",
        "color-brand-primary": "#fff",
        "color-brand-content": "#06C",
        "color-sidebar-link-text": "#f7f7f7",
        "color-sidebar-item-background--current": "#666",
        "color-sidebar-item-background--hover": "#333",
        "color-admonition-background": "transparent",
        "color-admonition-title-background--note": "var(--color-background-primary)",
        "color-admonition-title-background--tip": "var(--color-background-primary)",
        "color-admonition-title-background--important": "var(--color-background-primary)",
        "color-admonition-title-background--caution": "var(--color-background-primary)",
        "color-admonition-title--note": "#24598F",
        "color-admonition-title--tip": "#24598F",
        "color-admonition-title--important": "#C7162B",
        "color-admonition-title--caution": "#F99B11",
        "color-highlighted-background": "#666",
        "color-link-underline": "var(--color-background-primary)",
        "color-link-underline--hover": "var(--color-background-primary)",
    },
}

html_context = {
    "github_url": "https://github.com/lxc/lxd",
    "github_version": "master",
    "github_folder": "/doc/",
    "github_filetype": "md",
    "discourse_prefix": "https://discuss.linuxcontainers.org/t/"
}

html_sidebars = {
    "**": [
        "sidebar/variant-selector.html",
        "sidebar/search.html",
        "sidebar/scroll-start.html",
        "sidebar/navigation.html",
        "sidebar/scroll-end.html",
    ]
}

source_suffix = ".md"

# List of patterns, relative to source directory, that match files and
# directories to ignore when looking for source files.
# This pattern also affects html_static_path and html_extra_path.
exclude_patterns = ['html', 'README.md']

# Open Graph configuration

ogp_site_url = "https://linuxcontainers.org/lxd/docs/latest/"
ogp_site_name = "LXD documentation"
ogp_image = "https://linuxcontainers.org/static/img/containers.png"

# Links to ignore when checking links

linkcheck_ignore = [
    'https://127.0.0.1:8443/1.0',
    'https://web.libera.chat/#lxc'
]

# Setup redirects (https://documatt.gitlab.io/sphinx-reredirects/usage.html)
redirects = {
    "index/index": "../index.html",
    "network-peers/index": "../howto/network_ovn_peers/index.html",
    "network-acls/index": "../howto/network_acls/index.html",
    "network-forwards/index": "../howto/network_forwards/index.html",
    "network-zones/index": "../howto/network_zones/index.html",
    "howto/storage_create_pool/index": "../storage_pools/index.html#create-a-storage-pool",
    "howto/storage_configure_pool/index": "../storage_pools/index.html#configure-storage-pool-settings",
    "howto/storage_view_pools/index": "../storage_pools/index.html#view-storage-pools",
    "howto/storage_resize_pool/index": "../storage_pools/index.html#resize-a-storage-pool",
    "howto/storage_create_bucket/index": "../storage_buckets/index.html#create-a-storage-bucket",
    "howto/storage_configure_bucket/index": "../storage_buckets/index.html#configure-storage-bucket-settings",
    "howto/storage_view_buckets/index": "../storage_buckets/index.html#view-storage-buckets",
    "howto/storage_resize_bucket/index": "../storage_buckets/index.html#resize-a-storage-bucket",
    "howto/storage_create_volume/index": "../storage_volumes/index.html#create-a-custom-storage-volume",
    "howto/storage_configure_volume/index": "../storage_volumes/index.html#configure-storage-volume-settings",
    "howto/storage_view_volumes/index": "../storage_volumes/index.html#view-storage-volumes",
    "howto/storage_resize_volume/index": "../storage_volumes/index.html#resize-a-storage-volume",
    "production-setup/index": "../explanation/performance_tuning/index.html",
    "containers/index": "../explanation/instances",
    "virtual-machines/index": "../explanation/instances",
    "preseed/index": "../howto/initialize/index.html#initialize-preseed",
    "configuration/index": "../server"
}
