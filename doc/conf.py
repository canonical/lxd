import sys
import os
import requests
from urllib.parse import urlparse
from git import Repo, InvalidGitRepositoryError
import time

sys.path.append('./')
from custom_conf import *
sys.path.append('.sphinx/')
from build_requirements import *

# Configuration file for the Sphinx documentation builder.
# You should not do any modifications to this file. Put your custom
# configuration into the custom_conf.py file.
# If you need to change this file, contribute the changes upstream.
#
# For the full list of built-in configuration values, see the documentation:
# https://www.sphinx-doc.org/en/master/usage/configuration.html

############################################################
### Extensions
############################################################

extensions = [
    'sphinx_design',
    'sphinx_copybutton',
    'sphinxcontrib.jquery',
]

# Only add redirects extension if any redirects are specified.
if AreRedirectsDefined():
    extensions.append('sphinx_reredirects')

# Only add myst extensions if any configuration is present.
if IsMyStParserUsed():
    extensions.append('myst_parser')

    # Additional MyST syntax
    myst_enable_extensions = [
        'substitution',
        'deflist',
        'linkify'
    ]
    myst_enable_extensions.extend(custom_myst_extensions)

# Only add Open Graph extension if any configuration is present.
if IsOpenGraphConfigured():
    extensions.append('sphinxext.opengraph')

extensions.extend(custom_extensions)
extensions = DeduplicateExtensions(extensions)

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

# Default image for OGP (to prevent font errors, see
# https://github.com/canonical/sphinx-docs-starter-pack/pull/54 )
if not 'ogp_image' in locals():
    ogp_image = 'https://assets.ubuntu.com/v1/253da317-image-document-ubuntudocs.svg'

############################################################
### General configuration
############################################################

exclude_patterns = [
    '_build',
    'Thumbs.db',
    '.DS_Store',
    '.sphinx',
]
exclude_patterns.extend(custom_excludes)

rst_epilog = '''
.. include:: /reuse/links.txt
'''
if 'custom_rst_epilog' in locals():
    rst_epilog = custom_rst_epilog

source_suffix = {
    '.rst': 'restructuredtext',
    '.md': 'markdown',
}

if not 'conf_py_path' in html_context and 'github_folder' in html_context:
    html_context['conf_py_path'] = html_context['github_folder']

# For ignoring specific links
linkcheck_anchors_ignore_for_url = [
    r'https://github\.com/.*'
]
linkcheck_anchors_ignore_for_url.extend(custom_linkcheck_anchors_ignore_for_url)

# Tags cannot be added directly in custom_conf.py, so add them here
for tag in custom_tags:
    tags.add(tag)

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
    'footer.css'
]
html_css_files.extend(custom_html_css_files)

html_js_files = ['header-nav.js', 'footer.js']
if 'github_issues' in html_context and html_context['github_issues'] and not disable_feedback_button:
    html_js_files.append('github_issue_links.js')
html_js_files.extend(custom_html_js_files)

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
#############################################################
