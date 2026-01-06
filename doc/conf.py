import datetime
import os
import yaml

############################
# LXD custom configuration #
############################

import sys
import subprocess
from git import Repo, InvalidGitRepositoryError
import filecmp
import ast

sys.path.insert(0, os.path.abspath('.'))
from redirects import redirects

sys.path.append('.sphinx/')

# Set global version variable used in objects.inv to numeric version defined in flex.go
with open("../shared/version/flex.go") as fd:
    version = fd.readlines()[3].split()[-1].strip("\"")


#######################
# Project information #
#######################

# Project name
project = 'LXD'
author = 'LXD contributors'

# Sidebar documentation title; best kept reasonably short
# To disable the title, set to an empty string.
html_title = ''

# Copyright string; shown at the bottom of the page
copyright = '2014-%s AGPL-3.0, %s' % (datetime.date.today().year, author)

# Documentation website URL
#
# NOTE: The Open Graph Protocol (OGP) enhances page display in a social graph
#       and is used by social media platforms; see https://ogp.me/
ogp_site_url = 'https://documentation.ubuntu.com/lxd/latest/'

# Preview name of the documentation website
ogp_site_name = project + " documentation"

# Preview image URL
ogp_image = 'https://documentation.ubuntu.com/lxd/latest/_static/tag.png'

# Product favicon; shown in bookmarks, browser tabs, etc.
html_favicon = '.sphinx/_static/favicon.ico'

# Dictionary of values to pass into the Sphinx context for all pages:
# https://www.sphinx-doc.org/en/master/usage/configuration.html#confval-html_context
html_context = {
    # Product page URL; can be different from product docs URL
    'product_page': 'canonical.com/lxd',

    # Product tag image; the orange part of your logo, shown in the page header
    'product_tag': '_static/tag.png',

    # Your Discourse instance URL
    # NOTE: If set, adding ':discourse: 123' to an .rst file
    #       will add a link to Discourse topic 123 at the bottom of the page.
    'discourse': 'https://discourse.ubuntu.com/c/lxd/',

    # LXD docs refer to two different Discourse instances
    'discourse_prefix': {
        'ubuntu': 'https://discourse.ubuntu.com/t/',
        'lxc': 'https://discuss.linuxcontainers.org/t/'
    },

    # Your Mattermost channel URL
    'mattermost': '',

    # Your Matrix channel URL
    'matrix': 'https://matrix.to/#/#documentation:ubuntu.com',

    # Your documentation GitHub repository URL
    # NOTE: If set, links for viewing the documentation source files
    #       and creating GitHub issues are added at the bottom of each page.
    'github_url': 'https://github.com/canonical/lxd',

    # Docs branch in the repo; used in links for viewing the source files
    'github_version': 'main',

    # Docs location in the repo; used in links for viewing the source files
    'github_folder': '/doc/',

    # Enables the Previous / Next buttons at the bottom of pages
    # NOTE: Valid options are none, prev, next, both
    "sequential_nav": "both",

    # Enables listing contributors on individual pages
    "display_contributors": True,

    # Required for feedback button
    'github_issues': 'enabled',
}

# Project slug
# Required if your project is hosted on documentation.ubuntu.com
slug = "lxd"

#######################
# Sitemap configuration: https://sphinx-sitemap.readthedocs.io/
#######################

# Use RTD canonical URL to ensure duplicate pages have a single canonical URL
# that includes the version (such as /latest/); helps SEO.
# See: https://docs.readthedocs.com/platform/stable/canonical-urls.html and
# https://www.sphinx-doc.org/en/master/usage/configuration.html#confval-html_baseurl
# Second argument is for local builds where READTHEDOCS_CANONICAL_URL is not available
html_baseurl = os.environ.get("READTHEDOCS_CANONICAL_URL", "/")

# The sitemap extension uses html_baseurl to generate the full URL for each page
sitemap_url_scheme = '{link}'

# Include `lastmod` dates in the sitemap:
sitemap_show_lastmod = True

# Exclude generated pages from the sitemap:
sitemap_excludes = [
    '404/',
    'genindex/',
    'search/',
]

#############
# Redirects #
#############

# To set up redirects: https://documatt.gitlab.io/sphinx-reredirects/usage.html
# For example: 'explanation/old-name.html': '../how-to/prettify.html',

# To set up redirects in the Read the Docs project dashboard:
# https://docs.readthedocs.io/en/stable/guides/redirects.html

# NOTE: If undefined, set to None, or empty,
#       the sphinx_reredirects extension will be disabled.
# redirects = {}

# NOTE: LXD imports redirects from redirects.py


###########################
# Link checker exceptions #
###########################

# A regex list of URLs that are ignored by 'make linkcheck'

# Always ignore these links
linkcheck_ignore = [
    r"https?://localhost.*",
    r"https?://127\.0\.0\.1.*",
    r"^/.*/api/",
    # These links often/always fail both locally and in GitHub CI
    r"https://ceph\.io.*",
    r"https://.*\.sourceforge\.net.*",
    r"https://www\.gnu\.org.*",
    # These links often fail due to infra issues
    r"https://.*\.canonical\.com.*",
    r"https://snapcraft\.io.*",
    r"https://ubuntu\.com.*",
    r"https://.*\.launchpad\.net.*",
    # Ignore so that we can link change log in release notes before a release is ready
    r"https://github\.com/canonical/lxd/compare.*",
    r'https://kubernetes\.io/.*',
]

# Ignore these links in GitHub CI due to site restrictions causing failures
# In local checks, they are not ignored and should pass
if os.environ.get("CI") == "true":
    linkcheck_ignore.extend([
        r"https://www\.hpe\.com.*",
        r"https://www\.schlachter\.tech.*",
        r"https://www\.dell\.com.*",
    ])

# Pages on which to ignore anchors
# (This list will be appended to linkcheck_anchors_ignore_for_url)
linkcheck_anchors_ignore_for_url = [
    r'https://github\.com/.*',
    r'https://snapcraft\.io/docs/.*',
    'https://docs.docker.com/network/packet-filtering-firewalls/',
    'https://maas.io/docs/how-to-manage-machines',
    'https://web.libera.chat'
]

linkcheck_exclude_documents = [r'.*/manpages/.*']

# Increase linkcheck rate limit timeout max; default when unset is 300
# https://www.sphinx-doc.org/en/master/usage/configuration.html#confval-linkcheck_timeout
linkcheck_rate_limit_timeout = 600

# Increase linkcheck retries; default when unset is 1
# https://www.sphinx-doc.org/en/master/usage/configuration.html#confval-linkcheck_retries
linkcheck_retries = 3

########################
# Configuration extras #
########################

extensions = [
    'notfound.extension',
    'sphinx_design',
    'sphinx_reredirects',
    'sphinx_tabs.tabs',
    'sphinxcontrib.jquery',
    'sphinxext.opengraph',
    'sphinx_copybutton',
    'sphinx_config_options',
    'sphinx_related_links',
    'sphinx_roles',
    'sphinx_terminal',
    'sphinx_youtube_links',
    'sphinxcontrib.cairosvgconverter',
    'sphinx_last_updated_by_git',
    'sphinx.ext.intersphinx',
    'sphinx_sitemap',
    'sphinx_remove_toctrees',
    'myst_parser',
]

# Additional MyST syntax
myst_enable_extensions = [
    'substitution',
    'deflist',
    'linkify',
    'attrs_block',
]


### Configuration for extensions

# Used for related links
if not 'discourse_prefix' in html_context and 'discourse' in html_context:
    html_context['discourse_prefix'] = html_context['discourse'] + '/t/'

# The URL prefix for the notfound extension depends on whether the documentation uses versions.
# For documentation on documentation.ubuntu.com, we also must add the slug.
url_version = ''
url_lang = ''

# Determine if the URL uses versions and language
if 'READTHEDOCS_CANONICAL_URL' in os.environ and os.environ['READTHEDOCS_CANONICAL_URL']:
    url_parts = os.environ['READTHEDOCS_CANONICAL_URL'].split('/')

    if len(url_parts) >= 2 and 'READTHEDOCS_VERSION' in os.environ and os.environ['READTHEDOCS_VERSION'] == url_parts[-2]:
        url_version = url_parts[-2] + '/'

    if len(url_parts) >= 3 and 'READTHEDOCS_LANGUAGE' in os.environ and os.environ['READTHEDOCS_LANGUAGE'] == url_parts[-3]:
        url_lang = url_parts[-3] + '/'

# Set notfound_urls_prefix to the slug (if defined) and the version/language affix
if slug:
    notfound_urls_prefix = '/' + slug  + '/' + url_lang + url_version
elif len(url_lang + url_version) > 0:
    notfound_urls_prefix = '/' + url_lang + url_version
else:
    notfound_urls_prefix = ''

notfound_context = {
    'title': 'Page not found',
    'body': '<p><strong>Sorry, but the documentation page that you are looking for was not found.</strong></p>\n\n<p>Documentation changes over time, and pages are moved around. We try to redirect you to the updated content where possible, but unfortunately, that didn\'t work this time (maybe because the content you were looking for does not exist in this version of the documentation).</p>\n<p>You can try to use the navigation to locate the content you\'re looking for, or search for a similar page.</p>\n',
}

exclude_patterns = [
    '_build',
    'Thumbs.db',
    '.DS_Store',
    '.sphinx',
    'html',
    'README.md',
    'config_options_cheat_sheet.md'
]

# By default, the documentation includes a feedback button at the top.
# You can disable it by setting the following configuration to True.
disable_feedback_button = False

# Specifies a reST snippet to be prepended to each .rst file
# Defines woke-ignore and vale-ignore roles that can be used to mark content to be
# ignored by vale and woke checks

rst_prolog = """
.. role:: woke-ignore
    :class: woke-ignore
.. role:: vale-ignore
    :class: vale-ignore
"""

source_suffix = {
    '.rst': 'restructuredtext',
    '.md': 'markdown',
}

if not 'conf_py_path' in html_context and 'github_folder' in html_context:
    html_context['conf_py_path'] = html_context['github_folder']

# html_context['get_contribs'] is a function and cannot be
# cached (see https://github.com/sphinx-doc/sphinx/issues/12300)
suppress_warnings = ["config.cache"]

############################################################
### Styling
############################################################

# Find the current builder
builder = 'dirhtml'
if '-b' in sys.argv:
    builder = sys.argv[sys.argv.index('-b')+1]

# Setting templates_path for epub makes the build fail
if builder == 'dirhtml' or builder == 'html':
    templates_path = ['.sphinx/_templates']
    notfound_template = '404.html'

# Theme configuration
html_theme = 'furo'
html_last_updated_fmt = ''
html_permalinks_icon = '¶'

if html_title == '':
    html_theme_options = {
        'sidebar_hide_name': True
        }

############################################################
### Additional files
############################################################

html_static_path = ['.sphinx/_static']

html_css_files = [
    'custom.css',
    'header.css',
    'github_issue_links.css',
    'furo_colors.css',
    'footer.css',
    'cookie-banner.css',
]

html_js_files = ['header-nav.js', 'footer.js', 'js/bundle.js']
if 'github_issues' in html_context and html_context['github_issues'] and not disable_feedback_button:
    html_js_files.append('github_issue_links.js')

#############################################################
# Display the contributors

def get_contributors_for_file(github_url, github_folder, pagename, page_source_suffix, display_contributors_since=None):
    filename = f"{pagename}{page_source_suffix}"
    paths=html_context['github_folder'][1:] + filename

    try:
        repo = Repo(".")
    except InvalidGitRepositoryError:
        cwd = os.getcwd()
        ghfolder = html_context['github_folder'][:-1]
        if ghfolder and cwd.endswith(ghfolder):
            repo = Repo(cwd.rpartition(ghfolder)[0])
        else:
            print("The local Git repository could not be found.")
            return

    since = display_contributors_since if display_contributors_since and display_contributors_since.strip() else None

    commits = repo.iter_commits(paths=paths, since=since)

    contributors_dict = {}
    for commit in commits:
        contributor = commit.author.name
        if contributor not in contributors_dict or commit.committed_date > contributors_dict[contributor]['date']:
            contributors_dict[contributor] = {
                'date': commit.committed_date,
                'sha': commit.hexsha
            }
    # The github_page contains the link to the contributor's latest commit.
    contributors_list = [{'name': name, 'github_page': f"{github_url}/commit/{data['sha']}"} for name, data in contributors_dict.items()]
    sorted_contributors_list = sorted(contributors_list, key=lambda x: x['name'])
    return sorted_contributors_list

html_context['get_contribs'] = get_contributors_for_file

############################################################
### PDF configuration
############################################################

latex_additional_files = [
    "./.sphinx/fonts/Ubuntu-B.ttf",
    "./.sphinx/fonts/Ubuntu-R.ttf",
    "./.sphinx/fonts/Ubuntu-RI.ttf",
    "./.sphinx/fonts/UbuntuMono-R.ttf",
    "./.sphinx/fonts/UbuntuMono-RI.ttf",
    "./.sphinx/fonts/UbuntuMono-B.ttf",
    "./.sphinx/images/Canonical-logo-4x.png",
    "./.sphinx/images/front-page-light.pdf",
    "./.sphinx/images/normal-page-footer.pdf",
]

latex_engine = 'xelatex'
latex_show_pagerefs = True
latex_show_urls = 'footnote'

with open(".sphinx/latex_elements_template.txt", "rt") as file:
    latex_config = file.read()

latex_elements = ast.literal_eval(latex_config.replace("$PROJECT", project))

############################################################
### Misc LXD custom configuration
############################################################

# Prevents making links from URLs that do not start with a protocol
myst_linkify_fuzzy_links = False
# Auto-generate HTML anchors down to heading level 7
# https://myst-parser.readthedocs.io/en/latest/syntax/optional.html#auto-generated-header-anchors
myst_heading_anchors = 7

if os.path.exists('./substitutions.yaml'):
    with open('./substitutions.yaml', 'r') as fd:
        myst_substitutions = yaml.safe_load(fd.read())
if os.path.exists('./related_topics.yaml'):
    with open('./related_topics.yaml', 'r') as fd:
        myst_substitutions.update(yaml.safe_load(fd.read()))

if ('LOCAL_SPHINX_BUILD' in os.environ) and (os.environ['LOCAL_SPHINX_BUILD'] == 'True'):
    swagger_url_scheme = '/api/#{{path}}'
else:
    swagger_url_scheme = '/lxd/latest/api/#{{path}}'

myst_url_schemes = {
    'http': None,
    'https': None,
    'swagger': swagger_url_scheme,
}

remove_from_toctrees = ['reference/manpages/lxc/*.md']

intersphinx_mapping = {
    'cloud-init': ('https://cloudinit.readthedocs.io/en/latest/', None),
    'imagebuilder': ('https://canonical-lxd-imagebuilder.readthedocs-hosted.com/en/latest/', None)
}

html_extra_path = ['.sphinx/_extra']


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

### MAN PAGES ###

# Find path to lxc client (different for local builds and on RTD)

if ('LOCAL_SPHINX_BUILD' in os.environ and
    os.environ['LOCAL_SPHINX_BUILD'] == 'True'):
    path = str(subprocess.check_output(['go', 'env', 'GOPATH'], encoding='utf-8').strip())
    lxc = os.path.join(path, 'bin', 'lxc')
    if os.path.isfile(lxc):
        print('Using ' + lxc + ' to generate man pages.')
    else:
        print('Cannot find lxc in ' + lxc)
        exit(2)
else:
    lxc = '../lxc.bin'

# Generate man pages content

os.makedirs('.sphinx/deps/manpages', exist_ok=True)
if (os.path.isfile(lxc)):
    subprocess.run([lxc, 'manpage', '.sphinx/deps/manpages/', '--format=md'],
                   check=True)
else:
    print('No man page content generated.')

# Preprocess man pages content

for page in [x for x in os.listdir('.sphinx/deps/manpages')
             if os.path.isfile(os.path.join('.sphinx/deps/manpages/', x))]:

    # replace underscores with slashes to create a directory structure
    pagepath = page.replace('_', '/')

    # for each generated page, add an anchor, fix the title, and adjust the
    # heading levels
    with open(os.path.join('.sphinx/deps/manpages/', page), 'r') as mdfile:
        content = mdfile.readlines()

    os.makedirs(os.path.dirname(os.path.join('.sphinx/deps/manpages/', pagepath)),
                exist_ok=True)

    with open(os.path.join('.sphinx/deps/manpages/', pagepath), 'w') as mdfile:
        mdfile.write('(' + page + ')=\n')
        for line in content:
            if line.startswith('###### Auto generated'):
                continue
            elif line.startswith('## '):
                mdfile.write('# `' + line[3:].rstrip() + '`\n')
            elif line.startswith('##'):
                mdfile.write(line[1:])
            else:
                mdfile.write(line)

    # remove the input page (unless the file path doesn't change)
    if '_' in page:
        os.remove(os.path.join('.sphinx/deps/manpages/', page))

# Complete and copy man pages content

for folder, subfolders, files in os.walk('.sphinx/deps/manpages'):

    # for each subfolder, add toctrees to the parent page that
    # include the subpages
    for subfolder in subfolders:
        with open(os.path.join(folder, subfolder + '.md'), 'a') as parent:
            parent.write('```{toctree}\n:titlesonly:\n:glob:\n:hidden:\n\n' +
                         subfolder + '/*\n```\n')

    # for each file, if the content is different to what has been generated
    # before, copy the file to the reference/manpages folder
    # (copying all would mess up the incremental build)
    for f in files:
        sourcefile = os.path.join(folder, f)
        targetfile = os.path.join('reference/manpages/',
                                  os.path.relpath(folder,
                                                  '.sphinx/deps/manpages'),
                                  f)

        if (not os.path.isfile(targetfile) or
            not filecmp.cmp(sourcefile, targetfile, shallow=False)):

            os.makedirs(os.path.dirname(targetfile), exist_ok=True)
            os.system('cp ' + sourcefile + ' ' + targetfile)

### End MAN PAGES ###
