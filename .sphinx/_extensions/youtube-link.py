from docutils import nodes
from docutils.parsers.rst import Directive

class YouTubeLink(Directive):

    required_arguments = 1
    optional_arguments = 0
    has_content = False

    def run(self):

        fragment = ' \
        <p class="youtube_link"> \
          <a href="'+self.arguments[0]+'" target="_blank"> \
            <span class="play_icon">▶</span> \
            <span>Watch on YouTube</span> \
          </a> \
        </p>'
        raw = nodes.raw(text=fragment, format="html")

        return [raw]


def setup(app):
    app.add_directive("youtube", YouTubeLink)

    return {
        'version': '0.1',
        'parallel_read_safe': True,
        'parallel_write_safe': True,
    }
