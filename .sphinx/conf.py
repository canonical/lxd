import datetime
import os
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
    "sphinx_reredirects"
]

myst_enable_extensions = [
    "substitution",
    "deflist",
    "linkify"
]

if os.path.exists("doc/substitutions.yaml"):
    with open("doc/substitutions.yaml", "r") as fd:
        myst_substitutions = yaml.load(fd.read())

# Setup theme.
html_theme_path = ["themes"]
html_theme = "vanilla"
html_show_sphinx = False
html_last_updated_fmt = ""
html_favicon = "https://linuxcontainers.org/static/img/favicon.ico"
html_logo = "https://linuxcontainers.org/static/img/containers.small.png"

# Uses global TOC for side nav instead of default local TOC.
html_sidebars = {
    "**": [
        "globaltoc.html",
    ]
}

source_suffix = ".md"

# Setup redirects (https://documatt.gitlab.io/sphinx-reredirects/usage.html)
# redirects = {
#     "redirect/index": "../configuration/",
# }
