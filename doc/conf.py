import datetime
import os
import sys
import yaml
from git import Repo
import wget

# Download and link swagger-ui files
if not os.path.isdir('.sphinx/deps/swagger-ui'):
    Repo.clone_from('https://github.com/swagger-api/swagger-ui', '.sphinx/deps/swagger-ui', depth=1)

os.makedirs('.sphinx/_static/swagger-ui/', exist_ok=True)

if not os.path.islink('.sphinx/_static/swagger-ui/swagger-ui-bundle.js'):
    os.symlink('../../deps/swagger-ui/dist/swagger-ui-bundle.js', '.sphinx/_static/swagger-ui/swagger-ui-bundle.js')
if not os.path.islink('.sphinx/_static/swagger-ui/swagger-ui-standalone-preset.js'):
    os.symlink('../../deps/swagger-ui/dist/swagger-ui-standalone-preset.js', '.sphinx/_static/swagger-ui/swagger-ui-standalone-preset.js')
if not os.path.islink('.sphinx/_static/swagger-ui/swagger-ui.css'):
    os.symlink('../../deps/swagger-ui/dist/swagger-ui.css', '.sphinx/_static/swagger-ui/swagger-ui.css')

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
    "config-options",
    "notfound.extension",
    'sphinx_sitemap',
]

myst_enable_extensions = [
    "substitution",
    "deflist",
    "linkify"
]

myst_linkify_fuzzy_links=False
myst_heading_anchors = 7

if os.path.exists("./substitutions.yaml"):
    with open("./substitutions.yaml", "r") as fd:
        myst_substitutions = yaml.safe_load(fd.read())

intersphinx_mapping = {
    'cloud-init': ('https://cloudinit.readthedocs.io/en/latest/', None)
}

notfound_urls_prefix = "/lxd/en/latest/"

# Setup theme.
templates_path = [".sphinx/_templates"]

html_theme = "furo"
html_show_sphinx = False
html_last_updated_fmt = ""
html_favicon = ".sphinx/_static/favicon.ico"
html_static_path = ['.sphinx/_static']
html_css_files = ['custom.css']
html_js_files = ['header-nav.js']
html_extra_path = ['.sphinx/_extra']

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
        "color-version-popup": "#772953",
        "color-orange": "#FBDDD2",
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
        "color-version-popup": "#F29879",
        "color-orange": "#E95420",
    },
}

html_context = {
    "github_url": "https://github.com/canonical/lxd",
    "github_version": "stable-5.0",
    "github_folder": "/doc/",
    "github_filetype": "md",
    "discourse_prefix": {
        "lxc": "https://discuss.linuxcontainers.org/t/",
        "ubuntu": "https://discourse.ubuntu.com/t/"}
}

source_suffix = ".md"

# List of patterns, relative to source directory, that match files and
# directories to ignore when looking for source files.
# This pattern also affects html_static_path and html_extra_path.
exclude_patterns = ['html', 'README.md', '.sphinx', 'config_options_cheat_sheet.md']

# Open Graph configuration

ogp_site_url = "https://documentation.ubuntu.com/lxd/en/stable-5.0/"
ogp_site_name = "LXD documentation"
ogp_image = "https://documentation.ubuntu.com/lxd/en/stable-5.0/_static/tag.png"

# Links to ignore when checking links

linkcheck_ignore = [
    'https://127.0.0.1:8443',
    'https://127.0.0.1:8443/1.0',
    'https://web.libera.chat/#lxd',
    'http://localhost:8000',
    'http://localhost:8080',
    'http://localhost:8080/admin',
    r'/lxd/latest/api/.*',
    r'/api/.*',
    # Those links may fail from time to time
    'https://www.gnu.org/licenses/agpl-3.0.en.html',
    r"https://ceph\.io(/.*)?",
    # Blocked from GH runners
    'https://www.schlachter.tech/solutions/pongo2-template-engine/',
    r"https://.*\.sourceforge\.net/.*",
    r'https://docutils\.sourceforge\.io/docs/.*',
    r'https://.*canonical\.com/.*',
    r'https://snapcraft\.io/.*',
    r'https://ubuntu\.com/.*',
    r'https://bugs\.launchpad\.net/.*',
    r'https://microcloud\.is/.*',
]

# Ignore anchors for these URLs in linkcheck, but still check the URLs themselves
linkcheck_anchors_ignore_for_url = [
    'https://maas.io/docs/how-to-manage-machines',
]

# Setup redirects (https://documatt.gitlab.io/sphinx-reredirects/usage.html)
redirects = {
    "production-setup/index": "../explanation/performance_tuning/index.html",
}

#######################
# Sitemap configuration: https://sphinx-sitemap.readthedocs.io/
#######################

# Base URL of RTD hosted project

html_baseurl = 'https://documentation.ubuntu.com/lxd/'

# Configures URL scheme for sphinx-sitemap to generate correct URLs
# based on the version if built in RTD
if 'READTHEDOCS_VERSION' in os.environ:
    rtd_version = os.environ["READTHEDOCS_VERSION"]
    sitemap_url_scheme = f'{rtd_version}/{{link}}'
else:
    sitemap_url_scheme = '{link}'
