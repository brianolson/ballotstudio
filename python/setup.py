from setuptools import setup

setup(
    name='ballotstudio',
    version='0.1.0',
    description='ballotstudio setup and util',
    author='Brian Olson',
    author_email='bolson@bolson.org',
    url='https://github.com/brianolson/ballotstudio',
    packages=['ballotstudio'],
    package_dir={'ballotstudio': 'ballotstudio'},
    entry_points={
        'console_scripts':
        [
            'bsdraw = ballotstudio.draw:main',
        ]
    },
    license='AGPL 3.0',
    classifiers=[
        'Programming Language :: Python :: 3.8',
        'License :: OSI Approved :: GNU Affero General Public License v3 or later (AGPLv3+)',
    ]
)
