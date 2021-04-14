const sidebarElement = document.getElementById('sidebar')
const sidebarButton = document.getElementById('sidebar-button')
const sidebarLinks = sidebarElement.querySelectorAll('a')

sidebarButton.addEventListener('click', toggleSidebar)
Array.from(sidebarLinks).forEach((l) => {
	l.addEventListener('click', hideSidebar)
})

function showSidebar() {
	document.body.classList.add('overflow-hidden')
	sidebarElement.classList.remove('hidden')
}

function hideSidebar() {
	sidebarElement.classList.add('hidden')
	document.body.classList.remove('overflow-hidden')
}

function isShow() {
	return !sidebarElement.classList.contains('hidden')
}

function toggleSidebar() {
	if (isShow()) {
		hideSidebar()
	} else {
		showSidebar()
	}
}
