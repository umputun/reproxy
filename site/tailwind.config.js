const colors = require('tailwindcss/colors')

module.exports = {
	mode: 'jit',
	purge: {
		content: ['./src/**/*.njk', './src/**/*.js'],
		options: {
			safelist: ['mb-2', 'p-2', 'pl-4'],
		},
	},
	darkMode: 'media',
	theme: {
		extend: {
			colors: {
				orange: colors.orange,
			},
			typography: (theme) => ({
				dark: {
					css: [
						{
							color: theme('colors.gray.400'),
							'[class~="lead"]': {
								color: theme('colors.gray.300'),
							},
							a: {
								color: theme('colors.gray.200'),
							},
							strong: {
								color: theme('colors.gray.200'),
							},
							'ol > li::before': {
								color: theme('colors.gray.400'),
							},
							'ul > li::before': {
								backgroundColor: theme('colors.gray.600'),
							},
							hr: {
								borderColor: theme('colors.gray.300'),
							},
							blockquote: {
								color: theme('colors.gray.300'),
								borderLeftColor: theme('colors.gray.600'),
							},
							h1: {
								color: theme('colors.gray.200'),
							},
							h2: {
								color: theme('colors.gray.200'),
							},
							h3: {
								color: theme('colors.gray.200'),
							},
							h4: {
								color: theme('colors.gray.200'),
							},
							'figure figcaption': {
								color: theme('colors.gray.400'),
							},
							code: {
								color: theme('colors.gray.200'),
							},
							'a code': {
								color: theme('colors.gray.200'),
							},
							pre: {
								color: theme('colors.gray.300'),
								backgroundColor: theme('colors.gray.800'),
							},
							thead: {
								color: theme('colors.gray.200'),
								borderBottomColor: theme('colors.gray.400'),
							},
							'tbody tr': {
								borderBottomColor: theme('colors.gray.600'),
							},
						},
					],
				},
			}),
		},
	},
	variants: {
		extend: {
			typography: ['dark'],
		},
	},
	plugins: [
		require('@tailwindcss/typography')({
			modifiers: ['sm', 'md', 'lg'],
		}),
	],
}
