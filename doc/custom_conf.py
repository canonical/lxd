import datetime
import os
import sys
import subprocess
import yaml
from git import Repo
import filecmp

# Custom configuration for the Sphinx documentation builder.
# All configuration specific to your project should be done in this file.
#
# The file is included in the common conf.py configuration file.
# You can modify any of the settings below or add any configuration that
# is not covered by the common conf.py file.
#
# For the full list of built-in configuration values, see the documentation:
# https://www.sphinx-doc.org/en/master/usage/configuration.html
#
# If you're not familiar with Sphinx and don't want to use advanced
# features, it is sufficient to update the settings in the "Project
# information" section.

############################################################
### Project information
############################################################

# Product name
project = 'LXD'
author = 'LXD contributors'

# The title you want to display for the documentation in the sidebar.
# You might want to include a version number here.
# To not display any title, set this option to an empty string.
html_title = ''

with open("../shared/version/flex.go") as fd:
    version = fd.readlines()[3].split()[-1].strip("\"")

# The default value uses the current year as the copyright year.
#
# For static works, it is common to provide the year of first publication.
# Another option is to give the first year and the current year
# for documentation that is often changed, e.g. 2022–2023 (note the en-dash).
#
# A way to check a GitHub repo's creation date is to obtain a classic GitHub
# token with 'repo' permissions here: https://github.com/settings/tokens
# Next, use 'curl' and 'jq' to extract the date from the GitHub API's output:
#
# curl -H 'Authorization: token <TOKEN>' \
#   -H 'Accept: application/vnd.github.v3.raw' \
#   https://api.github.com/repos/canonical/<REPO> | jq '.created_at'

copyright = '2014-%s %s' % (datetime.date.today().year, author)

## Open Graph configuration - defines what is displayed as a link preview
## when linking to the documentation from another website (see https://ogp.me/)
# The URL where the documentation will be hosted (leave empty if you
# don't know yet)
# NOTE: If no ogp_* variable is defined (e.g. if you remove this section) the
# sphinxext.opengraph extension will be disabled.
ogp_site_url = 'https://documentation.ubuntu.com/lxd/en/latest/'
# The documentation website name (usually the same as the product name)
ogp_site_name = 'LXD documentation'
# The URL of an image or logo that is used in the preview
ogp_image = 'https://documentation.ubuntu.com/lxd/en/latest/_static/tag.png'

# Update with the local path to the favicon for your product
# (default is the circle of friends)
html_favicon = '.sphinx/_static/favicon.ico'

# (Some settings must be part of the html_context dictionary, while others
#  are on root level. Don't move the settings.)
html_context = {

    # Change to the link to the website of your product (without "https://")
    # For example: "ubuntu.com/lxd" or "microcloud.is"
    # If there is no product website, edit the header template to remove the
    # link (see the readme for instructions).
    'product_page': 'canonical.com/lxd',

    # Add your product tag (the orange part of your logo, will be used in the
    # header) to ".sphinx/_static" and change the path here (start with "_static")
    # (default is the circle of friends)
    'product_tag': '_static/tag.png',

    # Change to the discourse instance you want to be able to link to
    # using the :discourse: metadata at the top of a file
    # (use an empty value if you don't want to link)
    'discourse': 'https://discourse.ubuntu.com/c/lxd/',

    # ru-fu: we're using different Discourses
    'discourse_prefix': {
        'lxc': 'https://discuss.linuxcontainers.org/t/',
        'ubuntu': 'https://discourse.ubuntu.com/t/'
    },

    # Change to the Mattermost channel you want to link to
    # (use an empty value if you don't want to link)
    'mattermost': '',

    # Change to the Matrix channel you want to link to
    # (use an empty value if you don't want to link)
    'matrix': 'https://matrix.to/#/#documentation:ubuntu.com',

    # Change to the GitHub URL for your project
    # This is used, for example, to link to the source files and allow creating GitHub issues directly from the documentation.
    'github_url': 'https://github.com/canonical/lxd',

    # Change to the branch for this version of the documentation
    'github_version': 'main',

    # Change to the folder that contains the documentation
    # (usually "/" or "/docs/")
    'github_folder': '/doc/',

    # Change to an empty value if your GitHub repo doesn't have issues enabled.
    # This will disable the feedback button and the issue link in the footer.
    'github_issues': 'enabled',

    # Controls the existence of Previous / Next buttons at the bottom of pages
    # Valid options: none, prev, next, both
    'sequential_nav': "both",

    # Controls if to display the contributors of a file or not
    "display_contributors": True,

    # Controls time frame for showing the contributors
    "display_contributors_since": ""
}

# If your project is on documentation.ubuntu.com, specify the project
# slug (for example, "lxd") here.
slug = "lxd"

############################################################
### Redirects
############################################################

# Set up redirects (https://documatt.gitlab.io/sphinx-reredirects/usage.html)
# For example: 'explanation/old-name.html': '../how-to/prettify.html',
# You can also configure redirects in the Read the Docs project dashboard
# (see https://docs.readthedocs.io/en/stable/guides/redirects.html).
# NOTE: If this variable is not defined, set to None, or the dictionary is empty,
# the sphinx_reredirects extension will be disabled.
redirects = {
    'howto/instances_snapshots/index': '../instances_backup/',
    'reference/network_external/index': '../networks/',
    'explanation/containers_and_vms/index': '../instances/'
}

############################################################
### Link checker exceptions
############################################################

# Links to ignore when checking links
linkcheck_ignore = [
    'https://127.0.0.1:8443',
    'https://127.0.0.1:8443/1.0',
    'https://web.libera.chat/#lxd',
    'http://localhost:8000',
    r'/lxd/en/latest/api/.*',
    r'/api/.*',
    ]

# Pages on which to ignore anchors
# (This list will be appended to linkcheck_anchors_ignore_for_url)

custom_linkcheck_anchors_ignore_for_url = [
    r'https://snapcraft\.io/docs/.*',
    'https://docs.docker.com/network/packet-filtering-firewalls/'
    ]

linkcheck_exclude_documents = [r'.*/manpages/.*']

############################################################
### Additions to default configuration
############################################################

## The following settings are appended to the default configuration.
## Use them to extend the default functionality.

# Remove this variable to disable the MyST parser extensions.
custom_myst_extensions = []

# Add custom Sphinx extensions as needed.
# This array contains recommended extensions that should be used.
# NOTE: The following extensions are handled automatically and do
# not need to be added here: myst_parser, sphinx_copybutton, sphinx_design,
# sphinx_reredirects, sphinxcontrib.jquery, sphinxext.opengraph
custom_extensions = [
    'sphinx_tabs.tabs',
    'canonical.youtube-links',
    'canonical.related-links',
    'canonical.custom-rst-roles',
    'canonical.terminal-output',
    'notfound.extension',
    'sphinx.ext.intersphinx',
    'canonical.config-options',
    'sphinx_remove_toctrees',
    'canonical.filtered-toc',
    'sphinxcontrib.cairosvgconverter'
]

# Add custom required Python modules that must be added to the
# .sphinx/requirements.txt file.
# NOTE: The following modules are handled automatically and do not need to be
# added here: canonical-sphinx-extensions, furo, linkify-it-py, myst-parser,
# pyspelling, sphinx, sphinx-autobuild, sphinx-copybutton, sphinx-design,
# sphinx-notfound-page, sphinx-reredirects, sphinx-tabs, sphinxcontrib-jquery,
# sphinxext-opengraph
custom_required_modules = [
    'gitpython',
    'pyyaml',
    'sphinx-remove-toctrees',
    'sphinxcontrib-svg2pdfconverter[CairoSVG]'
]

# Add files or directories that should be excluded from processing.
custom_excludes = [
    'html',
    'README.md',
    'config_options_cheat_sheet.md'
]

# Add CSS files (located in .sphinx/_static/)
custom_html_css_files = []

# Add JavaScript files (located in .sphinx/_static/)
custom_html_js_files = []

## The following settings override the default configuration.

# Specify a reST string that is included at the end of each file.
# If commented out, use the default (which pulls the reuse/links.txt
# file into each reST file).
# custom_rst_epilog = ''

# By default, the documentation includes a feedback button at the top.
# You can disable it by setting the following configuration to True.
disable_feedback_button = False

# Add tags that you want to use for conditional inclusion of text
# (https://www.sphinx-doc.org/en/master/usage/restructuredtext/directives.html#tags)
custom_tags = []

# If you are using the :manpage: role, set this variable to the URL for the version
# that you want to link to:
# manpages_url = "https://manpages.ubuntu.com/manpages/noble/en/man{section}/{page}.{section}.html"

############################################################
### Additional configuration
############################################################

## Add any configuration that is not covered by the common conf.py file.

myst_linkify_fuzzy_links=False
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
    swagger_url_scheme = '/lxd/en/latest/api/#{{path}}'

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

if ('TOPICAL' in os.environ) and (os.environ['TOPICAL'] == 'True'):
    custom_excludes.extend(['tutorial/index.md','howto/index.md','explanation/index.md','reference/index.md','howto/troubleshoot.md'])
    redirects['index_topical/index'] = '../index.html'
    redirects['index_topical'] = '../index.html'
    custom_tags.append('topical')
    toc_filter_exclude = ['diataxis']
else:
    custom_excludes.extend(['security.md','external_resources.md','reference/network_external.md','migration.md'])
    redirects['security/index'] = '../explanation/security/'
    redirects['migration/index'] = '../howto/import_machines_to_instances/'
    custom_tags.append('diataxis')
    toc_filter_exclude = ['topical']


sys.path.append(os.path.abspath('.sphinx/_extensions/'))
