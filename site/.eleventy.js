const htmlmin = require('html-minifier')
const markdownIt = require('markdown-it')
const markdownItAnchor = require('markdown-it-anchor')
const toc = require('@thedigitalman/eleventy-plugin-toc-a11y')

function getVersion() {
	return `reproxy-${Date.now()}`
}

function transformHTML(content, outputPath) {
	if (!outputPath?.endsWith('.html')) {
		return content
	}

	return htmlmin.minify(content, {
		useShortDoctype: true,
		removeComments: true,
		collapseWhitespace: true,
	})
}

function transformMarkdown() {
	return markdownIt({
		html: true,
		breaks: true,
		linkify: true,
	}).use(markdownItAnchor, {
		permalink: true,
		permalinkClass: '',
		permalinkSymbol: '',
	})
}

module.exports = (config) => {
	config.setUseGitIgnore(false)
	config.addShortcode('version', getVersion)

	// Pluigns
	config.addPlugin(toc, {
		tags: ['h2', 'h3'],
		heading: false,
		listType: 'ul',
		wrapperClass: 'docs-nav',
		listClass: 'pl-5',
		listItemClass: 'mb-2',
		listItemAnchorClass:
			'inline-block p-1 hover:text-gray-900 dark:hover:text-gray-200',
	})

	// HTML transformations
	config.addTransform('htmlmin', transformHTML)

	// Markdown
	config.setLibrary('md', transformMarkdown())

	// Other files
	config.addPassthroughCopy('public/*')

	return {
		dir: {
			input: 'src',
			output: 'dist',
			data: 'data',
			layouts: 'layouts',
			includes: 'includes',
		},
	}
}
